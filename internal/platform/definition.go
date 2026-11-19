package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Parameter struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	In             string `json:"in"`
	Type           string `json:"type"`
	Required       bool   `json:"required"`
	NonEmpty       bool   `json:"nonEmpty,omitempty"`
	ItemArrayField string `json:"itemArrayField,omitempty"`
	Fixed          bool   `json:"fixed"`
	Value          string `json:"value"`
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
	if d.BodyTemplate != "" && (len(d.BodyTemplate) > 32768 || d.WholeBody) {
		return fmt.Errorf("请求体模板最多 32768 字节，且不能与完整请求体模式同时使用")
	}
	hasBody, hasContentType := d.WholeBody, false
	if d.WholeBody {
		if d.Method == "GET" || d.Method == "HEAD" {
			return fmt.Errorf("GET 和 HEAD 不支持请求体参数")
		}
		seen["body"] = true
	}
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
		if p.NonEmpty && (p.Type != "array" || p.Fixed) {
			return fmt.Errorf("仅客户端填写的数组参数可设置至少一项：%s", p.Name)
		}
		if p.ItemArrayField != "" && (p.Type != "array" || p.Fixed || !parameterName.MatchString(p.ItemArrayField)) {
			return fmt.Errorf("元素内非空数组字段无效：%s", p.Name)
		}
		switch p.In {
		case "query", "path", "header":
			if p.Type == "array" || p.Type == "object" {
				return fmt.Errorf("数组与对象参数只能放在 JSON 请求体中：%s", p.Name)
			}
		case "body":
			if d.WholeBody {
				return fmt.Errorf("使用完整请求体时，请移除请求体字段映射")
			}
			hasBody = true
			if d.BodyTemplate != "" {
				if !p.Fixed && !p.Required {
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
			if !strings.Contains(u.Path, "{"+p.Name+"}") || (!p.Fixed && !p.Required) {
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
		if _, err := renderBodyTemplate(d.BodyTemplate, templateParams); err != nil {
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

func (p Parameter) schema() map[string]any {
	schema := map[string]any{"type": p.Type, "description": p.Description}
	if p.Type == "integer" {
		schema["minimum"] = -9007199254740991
		schema["maximum"] = 9007199254740991
	}
	if p.Type == "array" {
		items := map[string]any{}
		if field := p.ItemArrayField; field != "" {
			items["type"] = "object"
			items["required"] = []string{field}
			items["properties"] = map[string]any{
				field: map[string]any{"type": "array", "minItems": 1, "items": map[string]any{}},
			}
		}
		schema["items"] = items
		if p.NonEmpty {
			schema["minItems"] = 1
		}
	}
	return schema
}

func (d Definition) InputSchema() map[string]any {
	properties := map[string]any{}
	if d.WholeBody {
		properties["body"] = map[string]any{"description": "完整的 JSON 请求体，省略时不发送请求体"}
	}
	required := []string{}
	for _, p := range d.Parameters {
		if p.Fixed {
			continue
		}
		properties[p.Name] = p.schema()
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func (d Definition) decodeArguments(data []byte) (map[string]any, error) {
	var args map[string]any
	if !d.WholeBody {
		err := json.Unmarshal(data, &args)
		return args, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, err
	}
	args = make(map[string]any, len(raw))
	for name, value := range raw {
		if name == "body" {
			args[name] = value
			continue
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, err
		}
		args[name] = decoded
	}
	return args, nil
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
		if d.WholeBody && name == "body" {
			continue
		}
		if _, ok := dynamic[name]; !ok {
			return nil, fmt.Errorf("不允许传入未知或固定参数：%s", name)
		}
	}
	address := d.URL
	query, headers, body := url.Values{}, http.Header{}, map[string]any{}
	for _, p := range d.Parameters {
		value, exists := args[p.Name]
		if p.Fixed {
			value, _ = fixedValue(p)
		} else {
			if !exists {
				if p.Required {
					return nil, fmt.Errorf("缺少必填参数：%s", p.Name)
				}
				continue
			}
			if !validType(value, p.Type) {
				if p.Type == "integer" {
					return nil, fmt.Errorf("参数 %s 应为安全范围内的整数；超过 ±9007199254740991 的 ID 请使用字符串类型", p.Name)
				}
				return nil, fmt.Errorf("参数 %s 的类型应为 %s", p.Name, p.Type)
			}
			if p.NonEmpty && len(value.([]any)) == 0 {
				return nil, fmt.Errorf("参数 %s 至少需要一项", p.Name)
			}
			if field := p.ItemArrayField; field != "" {
				for i, item := range value.([]any) {
					object, _ := item.(map[string]any)
					values, ok := object[field].([]any)
					if !ok || len(values) == 0 {
						return nil, fmt.Errorf("参数 %s[%d].%s 必须是至少包含一项的数组", p.Name, i, field)
					}
				}
			}
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
	if value, exists := args["body"]; d.WholeBody && exists {
		if raw, ok := value.(json.RawMessage); ok && json.Valid(raw) {
			data = raw
		} else {
			var err error
			data, err = json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("body 必须是有效的 JSON")
			}
		}
	} else if d.BodyTemplate != "" {
		var err error
		data, err = renderBodyTemplate(d.BodyTemplate, body)
		if err != nil {
			return nil, err
		}
	} else if len(body) > 0 {
		data, _ = json.Marshal(body)
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
