package platform

import (
	"encoding/json"
	"fmt"
	"strings"
)

type omittedTemplateArray struct{}

func renderBodyTemplate(source string, values map[string]any) ([]byte, []string, error) {
	if !json.Valid([]byte(source)) {
		return nil, nil, fmt.Errorf("请求体模板必须是有效的 JSON 对象")
	}
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return nil, nil, fmt.Errorf("请求体模板必须是有效的 JSON 对象")
	}

	used := map[string]bool{}
	var rendered []string
	var replace func(any) (any, bool, error)
	replace = func(node any) (any, bool, error) {
		hasOmission := false
		switch node := node.(type) {
		case map[string]any:
			for key, value := range node {
				nested, omitted, err := replace(value)
				if err != nil {
					return nil, false, err
				}
				hasOmission = hasOmission || omitted
				if _, missing := nested.(omittedTemplateArray); missing {
					delete(node, key)
				} else {
					node[key] = nested
				}
			}
		case []any:
			items := node[:0]
			for _, value := range node {
				start := len(rendered)
				nested, omitted, err := replace(value)
				if err != nil {
					return nil, false, err
				}
				if omitted {
					// 移除整条条件后，省略状态不再影响外层记录。
					if _, condition := nested.(map[string]any); condition {
						rendered = rendered[:start]
						continue
					}
					hasOmission = true
					if _, missing := nested.(omittedTemplateArray); missing {
						continue
					}
				}
				items = append(items, nested)
			}
			return items, hasOmission, nil
		case string:
			if strings.HasPrefix(node, "{{") && strings.HasSuffix(node, "}}") {
				name := strings.TrimSuffix(strings.TrimPrefix(node, "{{"), "}}")
				value, exists := values[name]
				if !exists {
					return nil, false, fmt.Errorf("请求体模板引用了未配置的请求体参数：%s", name)
				}
				used[name] = true
				_, omitted := value.(omittedTemplateArray)
				if !omitted {
					rendered = append(rendered, name)
				}
				return value, omitted, nil
			}
		}
		return node, hasOmission, nil
	}
	if _, _, err := replace(root); err != nil {
		return nil, nil, err
	}
	for name := range values {
		if !used[name] {
			return nil, nil, fmt.Errorf("请求体模板缺少参数占位符：%s", name)
		}
	}
	data, err := json.Marshal(root)
	return data, rendered, err
}
