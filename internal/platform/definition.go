package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Parameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	In          string `json:"in"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	QueryFilter bool   `json:"queryFilter,omitempty"`
	NonEmpty    bool   `json:"nonEmpty,omitempty"`

	Fixed bool   `json:"fixed"`
	Value string `json:"value"`
}

type Definition struct {
	Name           string        `json:"name"`
	Description    string        `json:"description"`
	URL            string        `json:"url"`
	Method         string        `json:"method"`
	TimeoutSeconds int           `json:"timeoutSeconds"`
	WholeBody      bool          `json:"wholeBody,omitempty"`
	BodyTemplate   string        `json:"bodyTemplate,omitempty"`
	Parameters     []Parameter   `json:"parameters"`
	OutputFields   []OutputField `json:"outputFields,omitempty"`
	Approval       *ApprovalRule `json:"approval,omitempty"`
}

var toolName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
var parameterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_.-]*)\}`)

func (d Definition) Validate() error {
	if !toolName.MatchString(d.Name) {
		return fmt.Errorf("工具名称须以字母或下划线开头，只能包含字母、数字、下划线和连字符，最长 64 字符")
	}
	if strings.TrimSpace(d.Description) == "" || len(d.Description) > 4000 {
		return fmt.Errorf("请填写工具用途，最长 4000 字节")
	}
	u, err := url.Parse(d.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || len(d.URL) > 4096 {
		return fmt.Errorf("请输入不含账号信息或片段的完整 HTTP/HTTPS 地址")
	}
	if _, err := url.ParseQuery(u.RawQuery); err != nil {
		return fmt.Errorf("URL 查询参数编码错误")
	}
	switch d.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		return fmt.Errorf("不支持的请求方法")
	}
	if d.TimeoutSeconds < 1 || d.TimeoutSeconds > 60 || len(d.Parameters) > 100 {
		return fmt.Errorf("超时须为 1–60 秒，最多配置 100 个参数")
	}
	seen, paths := map[string]bool{}, map[string]bool{}
	templateParams := map[string]any{}
	if d.WholeBody {
		return fmt.Errorf("完整请求体模式已移除，请配置请求体模板并重新保存")
	}
	if len(d.BodyTemplate) > 32768 {
		return fmt.Errorf("请求体模板最多 32768 字节")
	}
	hasBody, hasContentType := false, false
	for _, p := range d.Parameters {
		if !parameterName.MatchString(p.Name) || seen[strings.ToLower(p.Name)] {
			return fmt.Errorf("参数名称无效或重复：%s", p.Name)
		}
		seen[strings.ToLower(p.Name)] = true
		if len(p.Value) > 32768 || len(p.Description) > 2000 {
			return fmt.Errorf("参数内容过长：%s", p.Name)
		}
		switch p.Type {
		case "string", "boolean", "integer", "number", "object", "array":
		default:
			return fmt.Errorf("参数类型无效：%s", p.Name)
		}
		if p.NonEmpty && p.Fixed {
			return fmt.Errorf("仅客户端填写的参数可设置非空：%s", p.Name)
		}
		if p.QueryFilter && p.Fixed {
			return fmt.Errorf("仅客户端填写的参数可设为筛选参数：%s", p.Name)
		}
		switch p.In {
		case "query", "path", "header":
			if p.Type == "array" || p.Type == "object" {
				return fmt.Errorf("数组与对象参数只能放在 JSON 请求体中：%s", p.Name)
			}
		case "body":
			hasBody = true
			if d.BodyTemplate != "" {
				if !p.Fixed && !p.requiresValue() && p.Type != "array" {
					return fmt.Errorf("模板中的请求体参数必须为必填或固定值：%s", p.Name)
				}
				templateParams[p.Name] = nil
			}
			if d.Method == "GET" || d.Method == "HEAD" {
				return fmt.Errorf("GET 和 HEAD 不支持请求体参数")
			}
		default:
			return fmt.Errorf("参数位置无效：%s", p.Name)
		}
		if p.In == "header" {
			if strings.EqualFold(p.Name, "Content-Type") {
				hasContentType = true
			}
			switch strings.ToLower(p.Name) {
			case "host", "content-length", "connection", "transfer-encoding", "upgrade", "proxy-authorization", "proxy-connection":
				return fmt.Errorf("不允许覆盖连接控制请求头：%s", p.Name)
			}
		}
		if p.In == "path" {
			paths[p.Name] = true
			if !strings.Contains(u.Path, "{"+p.Name+"}") || (!p.Fixed && !p.requiresValue()) {
				return fmt.Errorf("路径参数须对应 URL 占位符且必填：%s", p.Name)
			}
		}
		if p.Fixed {
			v, err := fixedValue(p)
			if err != nil {
				return err
			}
			if p.In == "header" && strings.ContainsAny(valueText(v), "\r\n") {
				return fmt.Errorf("请求头不能包含换行")
			}
		}
	}
	if hasBody && hasContentType {
		return fmt.Errorf("JSON 请求体自动设置 Content-Type，请移除手动配置的同名请求头参数")
	}
	if d.BodyTemplate != "" {
		if len(templateParams) == 0 {
			return fmt.Errorf("请为请求体模板配置请求体参数")
		}
		if _, _, err := renderBodyTemplate(d.BodyTemplate, templateParams); err != nil {
			return err
		}
	}
	for _, match := range placeholder.FindAllStringSubmatch(u.Path, -1) {
		if !paths[match[1]] {
			return fmt.Errorf("缺少路径参数：%s", match[1])
		}
	}
	if strings.ContainsAny(placeholder.ReplaceAllString(u.Path, ""), "{}") {
		return fmt.Errorf("URL 路径占位符格式无效")
	}
	if err := d.Approval.validate(); err != nil {
		return err
	}
	return validateOutputFields(d.OutputFields)
}

func fixedValue(p Parameter) (any, error) {
	if p.Type == "string" {
		return p.Value, nil
	}
	var value any
	if err := json.Unmarshal([]byte(p.Value), &value); err != nil || !validType(value, p.Type) {
		return nil, fmt.Errorf("固定参数 %s 的值不符合 %s 类型", p.Name, p.Type)
	}
	return value, nil
}

func validType(v any, kind string) bool {
	switch kind {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer", "number":
		n, ok := v.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && (kind == "number" || (math.Trunc(n) == n && math.Abs(n) <= 9007199254740991))
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	}
	return false
}

func valueText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (p Parameter) requiresValue() bool {
	return p.Required || p.NonEmpty
}

func (p Parameter) schema() map[string]any {
	schema := map[string]any{"type": p.Type, "description": p.Description}
	if p.Type == "integer" {
		schema["minimum"] = -9007199254740991
		schema["maximum"] = 9007199254740991
	}
	if p.NonEmpty {
		switch p.Type {
		case "string":
			schema["pattern"] = `\S`
		case "object":
			schema["minProperties"] = 1
		}
	}
	if p.Type == "array" {
		schema["items"] = map[string]any{}
		if p.NonEmpty {
			schema["minItems"] = 1
		}
	}
	return schema
}

func (d Definition) InputSchema() map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, p := range d.Parameters {
		if p.Fixed {
			continue
		}
		properties[p.Name] = p.schema()
		if p.requiresValue() {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	if filters := d.queryFilterNames(); len(filters) > 0 {
		schema["description"] = queryFilterMessage(filters)
	}
	return schema
}

func (d Definition) queryFilterNames() []string {
	var names []string
	for _, p := range d.Parameters {
		if p.QueryFilter {
			names = append(names, p.Name)
		}
	}
	return names
}

func queryFilterMessage(names []string) string {
	return "请填写 " + strings.Join(names, " 或 ")
}

func hasQueryFilterValue(value any) bool {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		for _, item := range value {
			if hasQueryFilterValue(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range value {
			if hasQueryFilterValue(item) {
				return true
			}
		}
	default:
		return value != nil
	}
	return false
}

func (d Definition) decodeArguments(data []byte) (map[string]any, error) {
	var args map[string]any
	err := json.Unmarshal(data, &args)
	return args, err
}

func (d Definition) BuildRequest(args map[string]any) (*http.Request, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	dynamic := map[string]Parameter{}
	for _, p := range d.Parameters {
		if !p.Fixed {
			dynamic[p.Name] = p
		}
	}
	for name := range args {
		if _, ok := dynamic[name]; !ok {
			return nil, fmt.Errorf("不允许传入未知或固定参数：%s", name)
		}
	}
	filters := d.queryFilterNames()
	address := d.URL
	query, headers, body := url.Values{}, http.Header{}, map[string]any{}
	for _, p := range d.Parameters {
		value, exists := args[p.Name]
		if p.Fixed {
			value, _ = fixedValue(p)
		} else if exists {
			if !validType(value, p.Type) {
				if p.Type == "integer" {
					return nil, fmt.Errorf("参数 %s 应为安全范围内的整数；超过 ±9007199254740991 的 ID 请使用字符串类型", p.Name)
				}
				return nil, fmt.Errorf("参数 %s 的类型应为 %s", p.Name, p.Type)
			}
			optionalFilter := p.QueryFilter && !p.requiresValue()
			array, isArray := value.([]any)
			emptyOptionalArray := len(filters) > 0 && !p.requiresValue() && isArray && len(array) == 0
			if emptyOptionalArray || (optionalFilter && !hasQueryFilterValue(value)) {
				exists = false
			}
			if p.NonEmpty {
				switch v := value.(type) {
				case string:
					if strings.TrimSpace(v) == "" {
						return nil, fmt.Errorf("参数 %s 不能为空白", p.Name)
					}
				case map[string]any:
					if !hasQueryFilterValue(v) {
						return nil, fmt.Errorf("参数 %s 至少需要一个属性", p.Name)
					}
				case []any:
					if !hasQueryFilterValue(v) {
						return nil, fmt.Errorf("参数 %s 至少需要一项", p.Name)
					}
				}
			}
		}
		if !p.Fixed && !exists {
			if p.NonEmpty {
				return nil, fmt.Errorf("参数 %s 不能为空", p.Name)
			}
			if p.requiresValue() {
				return nil, fmt.Errorf("缺少必填参数：%s", p.Name)
			}
			if d.BodyTemplate != "" && p.In == "body" && p.Type == "array" {
				body[p.Name] = omittedTemplateArray{}
			}
			continue
		}
		switch p.In {
		case "path":
			text := valueText(value)
			if text == "" || text == "." || text == ".." {
				return nil, fmt.Errorf("路径参数不可为空或目录跳转：%s", p.Name)
			}
			address = strings.ReplaceAll(address, "{"+p.Name+"}", url.PathEscape(text))
		case "query":
			query.Set(p.Name, valueText(value))
		case "header":
			if strings.ContainsAny(valueText(value), "\r\n") {
				return nil, fmt.Errorf("请求头不能包含换行")
			}
			headers.Set(p.Name, valueText(value))
		case "body":
			body[p.Name] = value
		}
	}
	u, _ := url.Parse(address)
	q := u.Query()
	for key, values := range query {
		q[key] = values
	}
	u.RawQuery = q.Encode()
	var data []byte
	var renderedParameters []string
	if d.BodyTemplate != "" {
		var err error
		data, renderedParameters, err = renderBodyTemplate(d.BodyTemplate, body)
		if err != nil {
			return nil, err
		}
	} else if len(body) > 0 {
		data, _ = json.Marshal(body)
	}
	if len(filters) > 0 && !slices.ContainsFunc(filters, func(name string) bool {
		p := dynamic[name]
		return (d.BodyTemplate == "" || p.In != "body" || slices.Contains(renderedParameters, name)) &&
			hasQueryFilterValue(args[name])
	}) {
		return nil, fmt.Errorf("%s", queryFilterMessage(filters))
	}
	if len(data) > 0 {
		headers.Set("Content-Type", "application/json")
	}
	r, err := http.NewRequest(d.Method, u.String(), bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法创建请求")
	}
	r.Header = headers
	return r, nil
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("编号无效")
	}
	return id, nil
}
