package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
)

const (
	regexp2MatchTimeout = 50 * time.Millisecond
	maxSplitArrayRows   = 2000
)

// LogProcessor 参考阿里云 SLS（processor_json / processor_regex / processor_split_array / 字段保留与剔除）
// 对 logs 类型查询返回的日志行按顺序进行结构化解析、拆行与精简，再交给下游大模型分析。
type LogProcessor struct {
	Type                   string   `json:"type"`
	SourceKey              string   `json:"sourceKey,omitempty"`
	TargetKey              string   `json:"targetKey,omitempty"`
	KeepSource             bool     `json:"keepSource,omitempty"`
	KeepSourceIfParseError *bool    `json:"keepSourceIfParseError,omitempty"`
	ExpandDepth            int      `json:"expandDepth,omitempty"`
	ExpandConnector        string   `json:"expandConnector,omitempty"`
	Prefix                 string   `json:"prefix,omitempty"`
	Regex                  string   `json:"regex,omitempty"`
	Keys                   []string `json:"keys,omitempty"`
	Fields                 []string `json:"fields,omitempty"`
}

var autoLogSourceKeys = []string{"_msg", "Line", "line", "content", "message", "msg", "log", "body"}

func (p LogProcessor) keepSourceOnError() bool {
	if p.KeepSourceIfParseError == nil {
		return true
	}
	return *p.KeepSourceIfParseError
}

func normalizeLogProcessors(list []LogProcessor) []LogProcessor {
	if len(list) == 0 {
		return nil
	}
	out := make([]LogProcessor, len(list))
	for i, p := range list {
		p.Type = strings.ToLower(strings.TrimSpace(p.Type))
		p.SourceKey = strings.TrimSpace(p.SourceKey)
		p.TargetKey = strings.TrimSpace(p.TargetKey)
		p.Prefix = strings.TrimSpace(p.Prefix)
		if p.Type == "json" && p.ExpandConnector == "" {
			p.ExpandConnector = "."
		}
		if len(p.Keys) > 0 {
			keys := make([]string, 0, len(p.Keys))
			for _, k := range p.Keys {
				k = strings.TrimSpace(k)
				if k != "" {
					keys = append(keys, k)
				}
			}
			p.Keys = keys
		}
		if len(p.Fields) > 0 {
			fields := make([]string, 0, len(p.Fields))
			seen := map[string]bool{}
			for _, f := range p.Fields {
				f = strings.TrimSpace(f)
				if f != "" && !seen[f] {
					seen[f] = true
					fields = append(fields, f)
				}
			}
			p.Fields = fields
		}
		out[i] = p
	}
	return out
}

// compiledRegex 封装双引擎正则：优先使用 Go 标准库 RE2（线性时间、零回溯），
// 当包含前瞻/后顾等 RE2 不支持的语法时自动回退到 regexp2 并强制施加 50ms 匹配超时防 ReDoS。
type compiledRegex struct {
	std       *regexp.Regexp
	ext       *regexp2.Regexp
	groupNums []int
	names     []string
}

var pyNamedGroup = regexp.MustCompile(`\(\?P<([A-Za-z_][A-Za-z0-9_]*)>`)

func compileDualRegex(pattern string) (*compiledRegex, error) {
	if std, err := regexp.Compile(pattern); err == nil {
		subNames := std.SubexpNames()
		num := std.NumSubexp()
		names := make([]string, num)
		for i := 1; i <= num; i++ {
			names[i-1] = strings.TrimSpace(subNames[i])
		}
		return &compiledRegex{std: std, names: names}, nil
	}
	ext, err := regexp2.Compile(pattern, regexp2.None)
	if err != nil && strings.Contains(pattern, "(?P<") {
		converted := pyNamedGroup.ReplaceAllString(pattern, `(?<$1>`)
		ext, err = regexp2.Compile(converted, regexp2.None)
	}
	if err != nil {
		return nil, err
	}
	ext.MatchTimeout = regexp2MatchTimeout
	var nums []int
	var names []string
	for _, n := range ext.GetGroupNumbers() {
		if n <= 0 {
			continue
		}
		nums = append(nums, n)
		gName := ext.GroupNameFromNumber(n)
		if gName == strconv.Itoa(n) {
			gName = ""
		}
		names = append(names, strings.TrimSpace(gName))
	}
	return &compiledRegex{ext: ext, groupNums: nums, names: names}, nil
}

func (r *compiledRegex) numSubexp() int {
	return len(r.names)
}

func (r *compiledRegex) findSubmatch(text string) ([]string, bool) {
	if r.std != nil {
		m := r.std.FindStringSubmatch(text)
		if m == nil {
			return nil, false
		}
		return m[1:], true
	}
	m, err := r.ext.FindStringMatch(text)
	if err != nil || m == nil {
		return nil, false
	}
	out := make([]string, len(r.groupNums))
	for i, n := range r.groupNums {
		if g := m.GroupByNumber(n); g != nil {
			out[i] = g.String()
		}
	}
	return out, true
}

func resolveRegexFieldNames(re *compiledRegex, keys []string) ([]string, error) {
	num := re.numSubexp()
	if num == 0 {
		return nil, fmt.Errorf("正则表达式至少需要一个捕获组，例如 (\\S+) 或 (?P<ip>\\S+)")
	}
	if len(keys) > 0 && len(keys) != num {
		return nil, fmt.Errorf("提取字段数量（%d）须与正则捕获组数量（%d）一致", len(keys), num)
	}
	names := make([]string, num)
	hasAny := false
	for i := 0; i < num; i++ {
		var name string
		if len(keys) > 0 {
			name = strings.TrimSpace(keys[i])
		} else {
			name = re.names[i]
		}
		if len(name) > 128 {
			return nil, fmt.Errorf("提取字段名过长：%s", name)
		}
		names[i] = name
		if name != "" {
			hasAny = true
		}
	}
	if !hasAny {
		return nil, fmt.Errorf("请配置提取字段名，或在正则中使用命名捕获组 (?P<字段名>...)")
	}
	return names, nil
}

func validateLogProcessors(scope string, list []LogProcessor) error {
	if len(list) > 10 {
		return fmt.Errorf("%s 最多配置 10 个数据处理器", scope)
	}
	for i, p := range list {
		step := i + 1
		switch p.Type {
		case "json":
			if len(p.SourceKey) > 128 || len(p.ExpandConnector) > 8 || len(p.Prefix) > 64 {
				return fmt.Errorf("%s 第 %d 个 JSON 处理器字段名、连接符或前缀过长", scope, step)
			}
			if p.ExpandDepth < 0 || p.ExpandDepth > 20 {
				return fmt.Errorf("%s 第 %d 个 JSON 处理器展开深度须在 0–20 之间（0 表示全部展开）", scope, step)
			}
		case "split_array":
			if len(p.SourceKey) > 128 || len(p.TargetKey) > 128 {
				return fmt.Errorf("%s 第 %d 个数组拆分处理器字段名过长", scope, step)
			}
		case "regex":
			if len(p.SourceKey) > 128 {
				return fmt.Errorf("%s 第 %d 个正则处理器原始字段名过长", scope, step)
			}
			if p.Regex == "" || len(p.Regex) > 4096 {
				return fmt.Errorf("%s 第 %d 个正则处理器请输入不超过 4096 字节的正则表达式", scope, step)
			}
			re, err := compileDualRegex(p.Regex)
			if err != nil {
				return fmt.Errorf("%s 第 %d 个正则处理器表达式语法无效：%v", scope, step, err)
			}
			if _, err := resolveRegexFieldNames(re, p.Keys); err != nil {
				return fmt.Errorf("%s 第 %d 个正则处理器：%v", scope, step, err)
			}
		case "keep_fields", "drop_fields":
			if len(p.Fields) == 0 || len(p.Fields) > 100 {
				return fmt.Errorf("%s 第 %d 个字段过滤器请配置 1–100 个字段名", scope, step)
			}
			for _, f := range p.Fields {
				if strings.TrimSpace(f) == "" || len(f) > 128 {
					return fmt.Errorf("%s 第 %d 个字段过滤器存在空或过长的字段名", scope, step)
				}
			}
		default:
			return fmt.Errorf("%s 第 %d 个数据处理器类型无效：%s（仅支持 json、regex、split_array、keep_fields、drop_fields）", scope, step, p.Type)
		}
	}
	return nil
}

type compiledLogProcessor struct {
	spec       LogProcessor
	re         *compiledRegex
	regexNames []string
	fieldSet   map[string]bool
}

func compileLogProcessors(list []LogProcessor) ([]compiledLogProcessor, error) {
	normalized := normalizeLogProcessors(list)
	if err := validateLogProcessors("preview", normalized); err != nil {
		return nil, err
	}
	compiled := make([]compiledLogProcessor, 0, len(normalized))
	for _, p := range normalized {
		item := compiledLogProcessor{spec: p}
		switch p.Type {
		case "regex":
			re, err := compileDualRegex(p.Regex)
			if err != nil {
				return nil, err
			}
			names, err := resolveRegexFieldNames(re, p.Keys)
			if err != nil {
				return nil, err
			}
			item.re = re
			item.regexNames = names
		case "keep_fields", "drop_fields":
			item.fieldSet = make(map[string]bool, len(p.Fields))
			for _, f := range p.Fields {
				item.fieldSet[f] = true
			}
		}
		compiled = append(compiled, item)
	}
	return compiled, nil
}

func resolveLogSourceKey(row map[string]any, configured string) (string, any, bool) {
	if configured != "" {
		v, ok := row[configured]
		return configured, v, ok
	}
	for _, candidate := range autoLogSourceKeys {
		if v, ok := row[candidate]; ok {
			return candidate, v, true
		}
	}
	return "", nil, false
}

func cloneRow(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[k] = v
	}
	return out
}

func cloneRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = cloneRow(r)
	}
	return out
}

func flattenJSONMap(src map[string]any, prefix, connector string, maxDepth, currentDepth int, dst map[string]any) {
	for k, v := range src {
		key := k
		if prefix != "" {
			key = prefix + connector + k
		}
		if nested, ok := v.(map[string]any); ok && (maxDepth == 0 || currentDepth < maxDepth) {
			if len(nested) == 0 {
				dst[key] = nested
				continue
			}
			flattenJSONMap(nested, key, connector, maxDepth, currentDepth+1, dst)
			continue
		}
		dst[key] = v
	}
}

func (c compiledLogProcessor) applySingle(row map[string]any) (map[string]any, bool) {
	p := c.spec
	switch p.Type {
	case "json":
		key, raw, exists := resolveLogSourceKey(row, p.SourceKey)
		if !exists || raw == nil {
			return row, false
		}
		var obj map[string]any
		switch val := raw.(type) {
		case string:
			trimmed := strings.TrimSpace(val)
			if !strings.HasPrefix(trimmed, "{") {
				if !p.keepSourceOnError() {
					delete(row, key)
				}
				return row, false
			}
			dec := json.NewDecoder(strings.NewReader(trimmed))
			dec.UseNumber()
			if err := dec.Decode(&obj); err != nil || obj == nil {
				if !p.keepSourceOnError() {
					delete(row, key)
				}
				return row, false
			}
		case map[string]any:
			obj = val
		default:
			if !p.keepSourceOnError() {
				delete(row, key)
			}
			return row, false
		}
		if !p.KeepSource {
			delete(row, key)
		}
		connector := p.ExpandConnector
		if connector == "" {
			connector = "."
		}
		flattenJSONMap(obj, p.Prefix, connector, p.ExpandDepth, 1, row)
		return row, true

	case "regex":
		key, raw, exists := resolveLogSourceKey(row, p.SourceKey)
		if !exists || raw == nil {
			return row, false
		}
		text := valueText(raw)
		groups, ok := c.re.findSubmatch(text)
		if !ok {
			if !p.keepSourceOnError() {
				delete(row, key)
			}
			return row, false
		}
		if !p.KeepSource {
			delete(row, key)
		}
		for i, name := range c.regexNames {
			if name != "" && i < len(groups) {
				row[name] = groups[i]
			}
		}
		return row, true

	case "keep_fields":
		filtered := make(map[string]any, len(c.fieldSet))
		for k, v := range row {
			if c.fieldSet[k] {
				filtered[k] = v
			}
		}
		return filtered, true

	case "drop_fields":
		for k := range c.fieldSet {
			delete(row, k)
		}
		return row, true
	}
	return row, false
}

func (c compiledLogProcessor) applyRows(rows []map[string]any) ([]map[string]any, bool) {
	p := c.spec
	if p.Type != "split_array" {
		out := make([]map[string]any, len(rows))
		anyMatched := false
		for i, row := range rows {
			next, matched := c.applySingle(cloneRow(row))
			if matched {
				anyMatched = true
			}
			out[i] = next
		}
		return out, anyMatched
	}

	out := make([]map[string]any, 0, len(rows))
	anyMatched := false
	for _, row := range rows {
		if len(out) >= maxSplitArrayRows {
			break
		}
		current := cloneRow(row)
		key, raw, exists := resolveLogSourceKey(current, p.SourceKey)
		if !exists || raw == nil {
			out = append(out, current)
			continue
		}
		var arr []any
		switch val := raw.(type) {
		case []any:
			arr = val
		case string:
			trimmed := strings.TrimSpace(val)
			if !strings.HasPrefix(trimmed, "[") {
				if !p.keepSourceOnError() {
					delete(current, key)
				}
				out = append(out, current)
				continue
			}
			dec := json.NewDecoder(strings.NewReader(trimmed))
			dec.UseNumber()
			if err := dec.Decode(&arr); err != nil {
				if !p.keepSourceOnError() {
					delete(current, key)
				}
				out = append(out, current)
				continue
			}
		default:
			if !p.keepSourceOnError() {
				delete(current, key)
			}
			out = append(out, current)
			continue
		}

		anyMatched = true
		if !p.KeepSource {
			delete(current, key)
		}
		for _, elem := range arr {
			if len(out) >= maxSplitArrayRows {
				break
			}
			expanded := cloneRow(current)
			if p.TargetKey != "" {
				expanded[p.TargetKey] = elem
			} else if obj, ok := elem.(map[string]any); ok {
				for k, v := range obj {
					expanded[k] = v
				}
			} else {
				expanded["item"] = elem
			}
			out = append(out, expanded)
		}
	}
	return out, anyMatched
}

func applyLogProcessorsToRows(rows []map[string]any, list []LogProcessor) []map[string]any {
	if len(rows) == 0 || len(list) == 0 {
		return rows
	}
	compiled, err := compileLogProcessors(list)
	if err != nil || len(compiled) == 0 {
		return rows
	}
	current := cloneRows(rows)
	for _, proc := range compiled {
		current, _ = proc.applyRows(current)
	}
	return current
}

// applyProcessorsToOutputValue 对任意输出字段提取出的值（数组、对象或标量）应用处理器链。
func applyProcessorsToOutputValue(selected any, list []LogProcessor) (any, error) {
	if len(list) == 0 {
		return selected, nil
	}
	compiled, err := compileLogProcessors(list)
	if err != nil || len(compiled) == 0 {
		return selected, err
	}
	switch val := selected.(type) {
	case []any:
		if len(val) == 0 {
			return []any{}, nil
		}
		rows := make([]map[string]any, 0, len(val))
		for _, item := range val {
			if obj, ok := item.(map[string]any); ok {
				rows = append(rows, cloneRow(obj))
			} else {
				rows = append(rows, map[string]any{"content": item})
			}
		}
		for _, proc := range compiled {
			rows, _ = proc.applyRows(rows)
		}
		return rows, nil
	case map[string]any:
		rows := []map[string]any{cloneRow(val)}
		for _, proc := range compiled {
			rows, _ = proc.applyRows(rows)
		}
		return previewOutputValue(rows), nil
	default:
		rows := []map[string]any{{"content": val}}
		for _, proc := range compiled {
			rows, _ = proc.applyRows(rows)
		}
		return previewOutputValue(rows), nil
	}
}

func previewOutputValue(rows []map[string]any) any {
	if len(rows) == 1 {
		return rows[0]
	}
	return rows
}

type logProcessorPreviewStep struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Matched  bool   `json:"matched"`
	RowCount int    `json:"rowCount"`
	Output   any    `json:"output"`
}

func (a *App) previewLogProcessors(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Sample     string         `json:"sample"`
		SourceKey  string         `json:"sourceKey"`
		Processors []LogProcessor `json:"processors"`
	}
	if !decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Sample) == "" {
		fail(w, 400, "请输入一条日志样例用于验证")
		return
	}
	if len(input.Sample) > 65536 {
		fail(w, 400, "日志样例不能超过 65536 字节")
		return
	}
	compiled, err := compileLogProcessors(input.Processors)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(compiled) == 0 {
		fail(w, 400, "请先至少添加一个日志处理器")
		return
	}

	sourceKey := strings.TrimSpace(input.SourceKey)
	if sourceKey == "" {
		sourceKey = compiled[0].spec.SourceKey
	}
	if sourceKey == "" {
		sourceKey = "content"
	}

	var initialRow map[string]any
	trimmed := strings.TrimSpace(input.Sample)
	if strings.HasPrefix(trimmed, "{") {
		dec := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
		dec.UseNumber()
		var obj map[string]any
		if err := dec.Decode(&obj); err == nil && obj != nil {
			firstKey := compiled[0].spec.SourceKey
			if firstKey != "" {
				if _, ok := obj[firstKey]; ok {
					initialRow = obj
				}
			} else if compiled[0].spec.Type == "keep_fields" || compiled[0].spec.Type == "drop_fields" {
				initialRow = obj
			} else {
				for _, candidate := range autoLogSourceKeys {
					if _, ok := obj[candidate]; ok {
						initialRow = obj
						break
					}
				}
			}
		}
	}
	if initialRow == nil {
		initialRow = map[string]any{sourceKey: input.Sample}
	}

	current := []map[string]any{cloneRow(initialRow)}
	steps := make([]logProcessorPreviewStep, 0, len(compiled))
	for i, proc := range compiled {
		var matched bool
		current, matched = proc.applyRows(current)
		steps = append(steps, logProcessorPreviewStep{
			Index:    i,
			Type:     proc.spec.Type,
			Matched:  matched,
			RowCount: len(current),
			Output:   previewOutputValue(cloneRows(current)),
		})
	}
	writeJSON(w, 200, map[string]any{
		"input":    initialRow,
		"rowCount": len(current),
		"output":   previewOutputValue(current),
		"steps":    steps,
	})
}
