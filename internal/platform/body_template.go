package platform

import (
	"encoding/json"
	"fmt"
	"strings"
)

func renderBodyTemplate(source string, values map[string]any) ([]byte, error) {
	if !json.Valid([]byte(source)) {
		return nil, fmt.Errorf("请求体模板必须是有效的 JSON 对象")
	}
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return nil, fmt.Errorf("请求体模板必须是有效的 JSON 对象")
	}

	used := map[string]bool{}
	var replace func(any) (any, error)
	replace = func(node any) (any, error) {
		switch node := node.(type) {
		case map[string]any:
			for key, value := range node {
				nested, err := replace(value)
				if err != nil {
					return nil, err
				}
				node[key] = nested
			}
		case []any:
			for index, value := range node {
				nested, err := replace(value)
				if err != nil {
					return nil, err
				}
				node[index] = nested
			}
		case string:
			if strings.HasPrefix(node, "{{") && strings.HasSuffix(node, "}}") {
				name := strings.TrimSuffix(strings.TrimPrefix(node, "{{"), "}}")
				value, exists := values[name]
				if !exists {
					return nil, fmt.Errorf("请求体模板引用了未配置的请求体参数：%s", name)
				}
				used[name] = true
				return value, nil
			}
		}
		return node, nil
	}
	if _, err := replace(root); err != nil {
		return nil, err
	}
	for name := range values {
		if !used[name] {
			return nil, fmt.Errorf("请求体模板缺少参数占位符：%s", name)
		}
	}
	return json.Marshal(root)
}
