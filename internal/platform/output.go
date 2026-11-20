package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

type OutputField struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

var invalidPointerEscape = regexp.MustCompile(`~([^01]|$)`)
var pointerUnescape = strings.NewReplacer("~1", "/", "~0", "~")

func validateOutputFields(fields []OutputField) error {
	if len(fields) > 100 {
		return fmt.Errorf("最多配置 100 个输出字段")
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if field.Name == "" || field.Name != strings.TrimSpace(field.Name) || len(field.Name) > 128 || seen[field.Name] {
			return fmt.Errorf("输出字段名称为空、过长或重复：%q", field.Name)
		}
		seen[field.Name] = true
		if len(field.Value) > 32768 {
			return fmt.Errorf("输出字段 %q 的配置超过 32768 字节", field.Name)
		}
		switch field.Source {
		case "response":
			if !strings.HasPrefix(field.Value, "/") || invalidPointerEscape.MatchString(field.Value) {
				return fmt.Errorf("输出字段 %q 的来源路径须以 / 开头并使用有效的 JSON Pointer 转义", field.Name)
			}
		case "fixed":
			if !json.Valid([]byte(field.Value)) {
				return fmt.Errorf("输出字段 %q 的固定值必须是有效的 JSON", field.Name)
			}
		default:
			return fmt.Errorf("输出字段 %q 的来源无效", field.Name)
		}
	}
	return nil
}

func mapOutput(fields []OutputField, data []byte) ([]byte, error) {
	var body any
	decoded := false
	output := make(map[string]json.RawMessage, len(fields))
	size := 0
	for _, field := range fields {
		value := json.RawMessage(field.Value)
		if field.Source == "response" {
			if !decoded {
				decoder := json.NewDecoder(bytes.NewReader(data))
				decoder.UseNumber()
				if err := decoder.Decode(&body); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
					return nil, fmt.Errorf("上游响应不是有效的 JSON，无法提取输出字段 %q", field.Name)
				}
				decoded = true
			}
			selected, ok := outputValue(body, field.Value)
			if !ok {
				return nil, fmt.Errorf("输出字段 %q 找不到上游路径 %q", field.Name, field.Value)
			}
			encoded, err := json.Marshal(selected)
			if err != nil {
				return nil, fmt.Errorf("无法编码输出字段 %q", field.Name)
			}
			value = encoded
		}
		// 提前限制重复提取大对象时的内存占用。
		size += len(value)
		if size > maxResponseBytes {
			return nil, fmt.Errorf("自定义输出超过 2 MiB 限制")
		}
		output[field.Name] = value
	}
	result, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("无法编码自定义输出")
	}
	if len(result) > maxResponseBytes {
		return nil, fmt.Errorf("自定义输出超过 2 MiB 限制")
	}
	return result, nil
}

func outputValue(value any, pointer string) (any, bool) {
	for pointer != "" {
		part, rest, more := strings.Cut(pointer[1:], "/")
		pointer = ""
		if more {
			pointer = "/" + rest
		}
		part = pointerUnescape.Replace(part)
		switch node := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = node[part]
			if !ok {
				return nil, false
			}
		case []any:
			if part == "*" {
				selected := make([]any, len(node))
				for index, item := range node {
					var ok bool
					selected[index], ok = outputValue(item, pointer)
					if !ok {
						return nil, false
					}
				}
				return selected, true
			}
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) || strconv.Itoa(index) != part {
				return nil, false
			}
			value = node[index]
		default:
			return nil, false
		}
	}
	return value, true
}
