package platform

import (
	"strings"
	"testing"
)

func TestOutputFieldMapping(t *testing.T) {
	fields := []OutputField{
		{Name: "id", Source: "response", Value: "/data/0/a~1b~0"},
		{Name: "nullable", Source: "response", Value: "/data/0/nullable"},
		{Name: "source", Source: "fixed", Value: `"crm"`},
		{Name: "flags", Source: "fixed", Value: `[true, 0, {"active":false}]`},
	}
	d := sampleDefinition()
	d.OutputFields = fields
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"data":[{"a/b~":9007199254740993,"nullable":null,"ignored":1}]}`)
	got, err := mapOutput(fields, data)
	if want := `{"flags":[true,0,{"active":false}],"id":9007199254740993,"nullable":null,"source":"crm"}`; err != nil || string(got) != want {
		t.Fatalf("输出映射错误：%s，%v", got, err)
	}
	got, err = mapOutput(fields[2:], []byte(`纯文本`))
	if err != nil || string(got) != `{"flags":[true,0,{"active":false}],"source":"crm"}` {
		t.Fatalf("仅固定值时仍依赖上游 JSON：%s，%v", got, err)
	}
	for _, input := range []string{`{"data":[]}`, `{"data":[{}]}`, `null`, `{"data":[{"a/b~":1}]} true`, `纯文本`} {
		if got, err := mapOutput(fields, []byte(input)); err == nil || got != nil || !strings.Contains(err.Error(), `"id"`) {
			t.Errorf("响应 %q 未产生预期的字段错误：%s，%v", input, got, err)
		}
	}
	fields[1].Value = fields[0].Value
	large := []byte(`{"data":[{"a/b~":"` + strings.Repeat("x", maxResponseBytes/2) + `"}]}`)
	if _, err := mapOutput(fields, large); err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Fatalf("重复映射过大的响应未触发上限：%v", err)
	}
}

func TestDefinitionRejectsInvalidOutputFields(t *testing.T) {
	valid := OutputField{Name: "标识", Source: "response", Value: "/id"}
	for _, fields := range [][]OutputField{
		{valid, valid},
		{{Name: " ", Source: "response", Value: "/id"}},
		{{Name: "id", Source: "response", Value: "data.id"}},
		{{Name: "id", Source: "response", Value: "/~3"}},
		{{Name: "id", Source: "fixed", Value: `crm`}},
		{{Name: "id", Source: "unknown", Value: `null`}},
	} {
		d := sampleDefinition()
		d.OutputFields = fields
		if err := d.Validate(); err == nil {
			t.Errorf("接受了无效的输出字段：%+v", fields)
		}
	}
}

func TestOutputFieldsCanExtractEveryArrayItem(t *testing.T) {
	fields := []OutputField{
		{Name: "all", Source: "response", Value: "/data/list/*/id"},
		{Name: "first", Source: "response", Value: "/data/list/0/id"},
		{Name: "literal", Source: "response", Value: "/data/*"},
	}
	got, err := mapOutput(fields, []byte(`{"data":{"list":[{"id":9007199254740993},{"id":null}],"*":"literal"}}`))
	if err != nil || string(got) != `{"all":[9007199254740993,null],"first":9007199254740993,"literal":"literal"}` {
		t.Fatalf("应按原顺序提取，保留数字精度和 null：%s，%v", got, err)
	}
	got, err = mapOutput(fields[:1], []byte(`{"data":{"list":[]}}`))
	if err != nil || string(got) != `{"all":[]}` {
		t.Fatalf("空列表应提取为空数组：%s，%v", got, err)
	}
	if _, err := mapOutput(fields[:1], []byte(`{"data":{"list":[{"id":1},{}]}}`)); err == nil {
		t.Fatal("缺失字段的记录被静默跳过")
	}
}

func TestOutputFieldProcessorsForHTTPAndGrafana(t *testing.T) {
	// 1. 字符串数组提取（如 /results/B/frames/0/rows/*/_msg 或 HTTP 日志数组）+ 正则 -> JSON -> 拆数组 -> 保留字段
	fields := []OutputField{
		{
			Name:   "events",
			Source: "response",
			Value:  "/data/logs/*/raw",
			Processors: []LogProcessor{
				{Type: "regex", Regex: `^(\S+)\s+\[(\w+)\]\s+(\{.*\})$`, Keys: []string{"ip", "level", "payload"}},
				{Type: "json", SourceKey: "payload", ExpandDepth: 1},
				{Type: "split_array", SourceKey: "items"},
				{Type: "keep_fields", Fields: []string{"ip", "level", "code", "msg"}},
			},
		},
		{
			Name:   "single",
			Source: "response",
			Value:  "/data/summary",
			Processors: []LogProcessor{
				{Type: "regex", Regex: `(?<=latency=)(?P<ms>\d+)(?=ms)`},
			},
		},
	}
	if err := validateOutputFields(fields); err != nil {
		t.Fatalf("合法的输出字段处理器应通过校验：%v", err)
	}
	raw := []byte(`{
		"data": {
			"logs": [
				{"raw": "10.0.0.8 [ERROR] {\"items\":[{\"code\":\"E1\",\"msg\":\"timeout\",\"noise\":1},{\"code\":\"E2\",\"msg\":\"oom\",\"noise\":2}]}", "pod_uid": "drop"}
			],
			"summary": "host=db-1 latency=256ms ok"
		}
	}`)
	got, err := mapOutput(fields, raw)
	if err != nil {
		t.Fatalf("输出字段处理器执行失败：%v", err)
	}
	want := `{"events":[{"code":"E1","ip":"10.0.0.8","level":"ERROR","msg":"timeout"},{"code":"E2","ip":"10.0.0.8","level":"ERROR","msg":"oom"}],"single":{"ms":"256"}}`
	if string(got) != want {
		t.Fatalf("输出字段处理结果不符：\ngot:  %s\nwant: %s", got, want)
	}

	// 2. 固定值输出字段不允许配置处理器
	bad := []OutputField{
		{Name: "fixed_bad", Source: "fixed", Value: `"ok"`, Processors: []LogProcessor{{Type: "json"}}},
	}
	if err := validateOutputFields(bad); err == nil {
		t.Fatal("固定值输出字段配置处理器应被拒绝")
	}
}
