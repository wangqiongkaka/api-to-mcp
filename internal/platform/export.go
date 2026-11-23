package platform

import (
	"encoding/json"
	"net/url"
	"strings"
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
			if p.Fixed || p.requiresValue() {
				required = append(required, p.Name)
			}
		} else {
			parameters = append(parameters, map[string]any{"name": p.Name, "in": p.In, "description": p.Description, "required": p.Fixed || p.requiresValue() || p.In == "path", "schema": s})
		}
	}
	op := map[string]any{"operationId": d.Name, "summary": d.Description, "parameters": parameters, "responses": map[string]any{"default": map[string]any{"description": "上游接口响应；未声明响应结构。"}}}
	if d.BodyTemplate != "" {
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}}
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
