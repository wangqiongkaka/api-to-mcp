package platform

import (
	"encoding/json"
	"testing"
)

func TestLogProcessorPipelineSLSRegexJSONAndFilter(t *testing.T) {
	// 典型阿里云 SLS 场景：原始文本日志内嵌 JSON -> 正则拆出头信息与 JSON -> JSON 全部展开 -> 仅保留给大模型的精简字段
	rows := []map[string]any{
		{
			"Time":      "2026-11-28T07:00:00Z",
			"_msg":      `10.20.30.40 [ERROR] payload={"trace_id":"tr-1001","user":{"id":42,"role":"ops"},"msg":"db timeout","debug_blob":"very-noisy"}`,
			"pod_uid":   "uid-redundant-12345",
			"node_hash": "hash-redundant-999",
		},
		{
			// 第二条不匹配正则，默认应保留原始 _msg，不丢失异常日志
			"Time":    "2026-11-28T07:00:01Z",
			"_msg":    "plain unstructured fatal crash line",
			"pod_uid": "uid-redundant-12345",
		},
	}

	pipeline := []LogProcessor{
		{
			Type:  "regex",
			Regex: `^(\S+)\s+\[(\w+)\]\s+payload=(\{.*\})$`,
			Keys:  []string{"ip", "level", "payload"},
		},
		{
			Type:            "json",
			SourceKey:       "payload",
			ExpandDepth:     0,
			ExpandConnector: "_",
		},
		{
			Type:   "drop_fields",
			Fields: []string{"pod_uid", "node_hash", "debug_blob"},
		},
	}

	got := applyLogProcessorsToRows(rows, pipeline)
	if len(got) != 2 {
		t.Fatalf("应返回 2 行，实际 %d", len(got))
	}

	r0 := got[0]
	if _, exists := r0["_msg"]; exists {
		t.Fatalf("解析成功后默认应移除原始 _msg：%#v", r0)
	}
	if _, exists := r0["payload"]; exists {
		t.Fatalf("JSON 展开后默认应移除中间字段 payload：%#v", r0)
	}
	if _, exists := r0["debug_blob"]; exists {
		t.Fatalf("drop_fields 应移除 debug_blob：%#v", r0)
	}
	if r0["ip"] != "10.20.30.40" || r0["level"] != "ERROR" || r0["trace_id"] != "tr-1001" || r0["msg"] != "db timeout" {
		t.Fatalf("提取结果不符：%#v", r0)
	}
	if gotID, ok := r0["user_id"].(json.Number); !ok || gotID.String() != "42" {
		t.Fatalf("嵌套 JSON 未按连接符 _ 正确展开：%#v", r0["user_id"])
	}

	// 不匹配的第二行默认保留原始 _msg，但仍剔除了噪声列 pod_uid
	r1 := got[1]
	if r1["_msg"] != "plain unstructured fatal crash line" {
		t.Fatalf("不匹配行默认应保留原始 _msg：%#v", r1)
	}
	if _, exists := r1["pod_uid"]; exists {
		t.Fatalf("drop_fields 应移除 pod_uid：%#v", r1)
	}
}

func TestLogProcessorNamedRegexDepthAndKeepFields(t *testing.T) {
	dropOnErr := false
	rows := []map[string]any{
		{"Line": `host=192.168.1.5 code=502 data={"a":{"b":{"c":1}},"k":"v"}`},
		{"Line": `not-matching-line`},
	}
	pipeline := []LogProcessor{
		{
			Type:                   "regex",
			Regex:                  `host=(?P<host>\S+)\s+code=(?P<code>\d+)\s+data=(?P<data>\{.*\})`,
			KeepSourceIfParseError: &dropOnErr,
		},
		{
			Type:            "json",
			SourceKey:       "data",
			ExpandDepth:     1,
			ExpandConnector: ".",
			Prefix:          "j",
		},
		{
			Type:   "keep_fields",
			Fields: []string{"host", "code", "j.k", "j.a"},
		},
	}

	got := applyLogProcessorsToRows(rows, pipeline)
	r0 := got[0]
	if r0["host"] != "192.168.1.5" || r0["code"] != "502" || r0["j.k"] != "v" {
		t.Fatalf("命名捕获组或 JSON 前缀展开结果不符：%#v", r0)
	}
	// ExpandDepth=1 只展开第一层，j.a 应保持为嵌套对象
	if nested, ok := r0["j.a"].(map[string]any); !ok || nested["b"] == nil {
		t.Fatalf("ExpandDepth=1 应保留深层嵌套对象：%#v", r0["j.a"])
	}
	// 第二行显式设置了 KeepSourceIfParseError=false，且 keep_fields 后为空对象
	if len(got[1]) != 0 {
		t.Fatalf("第二行应被过滤为空对象：%#v", got[1])
	}
}

func TestValidateLogProcessorsRejectsInvalidConfigs(t *testing.T) {
	baseDef := func(dataType string, procs []LogProcessor) Definition {
		return Definition{
			Kind:           "grafana",
			Name:           "log_proc_check",
			Description:    "校验日志处理器",
			TimeoutSeconds: 15,
			Grafana: &GrafanaConfig{
				ConnectionID: 1,
				FromRequired: false,
				DefaultFrom:  "now-1h",
				ToRequired:   false,
				DefaultTo:    "now",
				Queries: []GrafanaQuery{
					{
						RefID:          "A",
						DatasourceUID:  "u1",
						DatasourceType: "victoriametrics-logs-datasource",
						DataType:       dataType,
						Expr:           "level:error",
						LogProcessors:  procs,
					},
				},
			},
		}
	}

	cases := []struct {
		name string
		def  Definition
	}{
		{"metrics 查询不允许配置日志处理器", baseDef("metrics", []LogProcessor{{Type: "json"}})},
		{"未知处理器类型", baseDef("logs", []LogProcessor{{Type: "xml"}})},
		{"无效正则语法", baseDef("logs", []LogProcessor{{Type: "regex", Regex: "[unclosed"}})},
		{"正则缺少捕获组", baseDef("logs", []LogProcessor{{Type: "regex", Regex: `\d+`}})},
		{"正则捕获组数量与字段数不匹配", baseDef("logs", []LogProcessor{{Type: "regex", Regex: `(\w+):(\d+)`, Keys: []string{"only_one"}}})},
		{"普通捕获组未指定字段名", baseDef("logs", []LogProcessor{{Type: "regex", Regex: `(\w+)`}})},
		{"keep_fields 为空", baseDef("logs", []LogProcessor{{Type: "keep_fields"}})},
		{"JSON 展开深度越界", baseDef("logs", []LogProcessor{{Type: "json", ExpandDepth: 21}})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.def.Validate(); err == nil {
				t.Fatalf("场景 %s 应校验失败", tc.name)
			}
		})
	}
}

func TestFormatGrafanaResponseAppliesLogProcessorsOnlyToLogs(t *testing.T) {
	raw := []byte(`{
		"results": {
			"A": {
				"status": 200,
				"frames": [{
					"schema": {"fields": [{"name":"Time","type":"time"},{"name":"Value","type":"number"}]},
					"data": {"values": [[1700000000000], [0.75]]}
				}]
			},
			"B": {
				"status": 200,
				"frames": [{
					"schema": {"fields": [{"name":"Time","type":"time"},{"name":"_msg","type":"string"},{"name":"noise","type":"string"}]},
					"data": {"values": [[1700000000000], ["{\"level\":\"error\",\"msg\":\"oom\",\"extra\":1}"], ["drop-me"]]}
				}]
			}
		}
	}`)
	queries := []GrafanaQuery{
		{RefID: "A", DatasourceType: "prometheus", DataType: "metrics"},
		{
			RefID:          "B",
			DatasourceType: "victoriametrics-logs-datasource",
			DataType:       "logs",
			LogProcessors: []LogProcessor{
				{Type: "json", ExpandDepth: 1},
				{Type: "keep_fields", Fields: []string{"Time", "level", "msg"}},
			},
		},
	}
	out, err := formatGrafanaResponse("now-15m", "now", queries, raw)
	if err != nil {
		t.Fatalf("格式化失败：%v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	results := parsed["results"].(map[string]any)
	bRows := results["B"].(map[string]any)["frames"].([]any)[0].(map[string]any)["rows"].([]any)
	row0 := bRows[0].(map[string]any)
	if row0["level"] != "error" || row0["msg"] != "oom" || len(row0) != 3 {
		t.Fatalf("Logs 处理器未按预期精简输出：%#v", row0)
	}
	if _, hasNoise := row0["noise"]; hasNoise {
		t.Fatalf("噪声字段未被剔除：%#v", row0)
	}
}

func TestLogProcessorLookaroundDualEngineRegex(t *testing.T) {
	rows := []map[string]any{
		{"content": "user=alice token=secret123 host=10.0.0.8 status=502"},
		{"content": "user=bob token=none host=10.0.0.9 status=200"},
	}
	// 使用 Go RE2 不支持的后顾 (?<=...) 和负向前瞻 (?!...) 断言 + Python 风格命名捕获组 (?P<...>)
	pipeline := []LogProcessor{
		{
			Type:  "regex",
			Regex: `(?<=host=)(?P<host>\S+)\s+status=(?!200)(?P<err_status>\d+)`,
		},
	}
	if err := validateLogProcessors("A", pipeline); err != nil {
		t.Fatalf("合法的前瞻/后顾正则应通过校验：%v", err)
	}
	got := applyLogProcessorsToRows(rows, pipeline)
	if got[0]["host"] != "10.0.0.8" || got[0]["err_status"] != "502" {
		t.Fatalf("前瞻/后顾正则首行提取失败：%#v", got[0])
	}
	if _, exists := got[0]["content"]; exists {
		t.Fatalf("首行匹配后默认应移除原始 content：%#v", got[0])
	}
	// 第二行 status=200 被负向前瞻 (?!200) 排除，默认保留原 content
	if got[1]["content"] != "user=bob token=none host=10.0.0.9 status=200" {
		t.Fatalf("第二行不匹配时应保留原始 content：%#v", got[1])
	}

	// 验证 .NET 命名捕获组 (?<name>...) 与正向前瞻 (?=...)
	dotNetGot := applyLogProcessorsToRows(
		[]map[string]any{{"content": "latency=128ms host=db-01"}},
		[]LogProcessor{{Type: "regex", Regex: `latency=(?<ms>\d+)(?=ms)`}},
	)
	if dotNetGot[0]["ms"] != "128" {
		t.Fatalf(".NET 命名捕获组与正向前瞻失败：%#v", dotNetGot[0])
	}
}

func TestLogProcessorSplitArrayAndChaining(t *testing.T) {
	// 场景 1：原始 JSON 字符串包含事件数组 -> JSON 展开 -> 拆数组为多行 -> 字段过滤
	rows := []map[string]any{
		{
			"Time": "2026-11-28T07:00:00Z",
			"host": "node-1",
			"_msg": `{"batch":"b-1","events":[{"id":1,"err":"timeout","extra":"x"},{"id":2,"err":"oom","extra":"y"}]}`,
		},
	}
	pipeline := []LogProcessor{
		{Type: "json", SourceKey: "_msg", ExpandDepth: 1},
		{Type: "split_array", SourceKey: "events"},
		{Type: "keep_fields", Fields: []string{"Time", "host", "batch", "id", "err"}},
	}
	got := applyLogProcessorsToRows(rows, pipeline)
	if len(got) != 2 {
		t.Fatalf("应拆出 2 行，实际 %d：%#v", len(got), got)
	}
	if got[0]["host"] != "node-1" || got[0]["batch"] != "b-1" || got[0]["err"] != "timeout" {
		t.Fatalf("第 1 行拆数组结果不符：%#v", got[0])
	}
	if _, hasExtra := got[0]["extra"]; hasExtra {
		t.Fatalf("keep_fields 未剔除 extra：%#v", got[0])
	}
	if got[1]["err"] != "oom" {
		t.Fatalf("第 2 行拆数组结果不符：%#v", got[1])
	}

	// 场景 2：原始字段本身为 JSON 数组字符串 + 标量元素指定 TargetKey
	scalarRows := []map[string]any{
		{"Time": "t1", "content": `["10.0.0.1","10.0.0.2"]`},
	}
	scalarGot := applyLogProcessorsToRows(scalarRows, []LogProcessor{
		{Type: "split_array", SourceKey: "content", TargetKey: "ip"},
	})
	if len(scalarGot) != 2 || scalarGot[0]["ip"] != "10.0.0.1" || scalarGot[1]["ip"] != "10.0.0.2" {
		t.Fatalf("标量数组拆分失败：%#v", scalarGot)
	}
}
