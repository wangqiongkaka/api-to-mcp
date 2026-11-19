package platform

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/higress-group/openapi-to-mcpserver/pkg/converter"
	"github.com/higress-group/openapi-to-mcpserver/pkg/models"
	"github.com/higress-group/openapi-to-mcpserver/pkg/parser"
	"gopkg.in/yaml.v3"
)

func (d Definition) OpenAPI() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	u, _ := url.Parse(d.URL)
	parameters := []any{}
	properties := map[string]any{}
	required := []string{}
	all := append([]Parameter{}, d.Parameters...)
	for key, values := range u.Query() {
		found := false
		for _, p := range all {
			if p.Name == key && p.In == "query" {
				found = true
			}
		}
		if !found {
			all = append(all, Parameter{Name: key, In: "query", Type: "string", Fixed: true, Value: values[0]})
		}
	}
	for _, p := range all {
		s := p.schema()
		if p.Fixed {
			v, _ := fixedValue(p)
			s["enum"] = []any{v}
			s["default"] = v
		}
		if p.In == "body" {
			properties[p.Name] = s
			if p.Fixed || p.Required {
				required = append(required, p.Name)
			}
		} else {
			parameters = append(parameters, map[string]any{"name": p.Name, "in": p.In, "description": p.Description, "required": p.Fixed || p.Required || p.In == "path", "schema": s})
		}
	}
	op := map[string]any{"operationId": d.Name, "summary": d.Description, "parameters": parameters, "responses": map[string]any{"default": map[string]any{"description": "上游接口响应；未声明响应结构。"}}}
	if d.WholeBody {
		op["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{}}}}
	} else if len(properties) > 0 {
		op["requestBody"] = map[string]any{"required": len(required) > 0, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": properties, "required": required}}}}
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	doc := map[string]any{"openapi": "3.0.3", "info": map[string]any{"title": d.Name, "version": "1.0.0"}, "servers": []any{map[string]any{"url": u.Scheme + "://" + u.Host}}, "paths": map[string]any{path: map[string]any{strings.ToLower(d.Method): op}}}
	return json.MarshalIndent(doc, "", "  ")
}

func (d Definition) Higress() ([]byte, error) {
	if d.WholeBody {
		return nil, fmt.Errorf("Higress 导出暂不支持完整 JSON 请求体")
	}
	data, err := d.OpenAPI()
	if err != nil {
		return nil, err
	}
	p := parser.NewParser()
	p.SetValidation(true)
	if err = p.Parse(data); err != nil {
		return nil, err
	}
	cfg, err := converter.NewConverter(p, models.ConvertOptions{ServerName: d.Name}).Convert()
	if err != nil {
		return nil, err
	}
	tool := &cfg.Tools[0]
	u, _ := url.Parse(d.URL)
	q := u.Query()
	remaining := []models.Arg{}
	for _, arg := range tool.Args {
		fixed := false
		for _, param := range d.Parameters {
			if param.Name != arg.Name || param.In != arg.Position || !param.Fixed {
				continue
			}
			v, _ := fixedValue(param)
			switch param.In {
			case "query":
				q.Set(param.Name, valueText(v))
				fixed = true
			case "path":
				// 保留转义路径，避免固定值中的斜线改变路径层级。
				path := strings.ReplaceAll(u.EscapedPath(), url.PathEscape("{"+param.Name+"}"), url.PathEscape(valueText(v)))
				u.Path, _ = url.PathUnescape(path)
				u.RawPath = path
				fixed = true
			case "header":
				tool.RequestTemplate.Headers = append(tool.RequestTemplate.Headers, models.Header{Key: param.Name, Value: valueText(v)})
				fixed = true
			case "body":
				arg.Default = v
				arg.Enum = []any{v}
				arg.Required = false
			}
		}
		if !fixed {
			if _, ok := q[arg.Name]; ok && arg.Position == "query" {
				isDynamic := false
				for _, p := range d.Parameters {
					if p.Name == arg.Name && p.In == "query" && !p.Fixed {
						isDynamic = true
					}
				}
				if !isDynamic {
					continue
				}
			}
			remaining = append(remaining, arg)
		}
	}
	u.RawQuery = q.Encode()
	tool.RequestTemplate.URL = u.String()
	tool.Args = remaining
	return yaml.Marshal(cfg)
}
