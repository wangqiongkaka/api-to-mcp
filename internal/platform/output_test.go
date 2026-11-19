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
