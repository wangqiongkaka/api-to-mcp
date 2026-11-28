package platform

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestValidateGrafanaConnectionAndAuth(t *testing.T) {
	name, baseURL, origin, creds, err := validateGrafanaConnection(grafanaConnectionInput{
		Name:     "生产 Grafana ",
		BaseURL:  "https://Grafana.Example.com/sub/",
		AuthType: "bearer",
		Token:    "glsa_secret_token",
	}, nil)
	if err != nil {
		t.Fatalf("合法 Bearer 连接应通过校验：%v", err)
	}
	if name != "生产 Grafana" || baseURL != "https://Grafana.Example.com/sub" || origin != "https://grafana.example.com" {
		t.Fatalf("规范化结果不符：%q %q %q", name, baseURL, origin)
	}
	req, _ := http.NewRequest("GET", baseURL+"/api/datasources", nil)
	if err := applyGrafanaAuth(req, "bearer", creds); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer glsa_secret_token" {
		t.Fatalf("Bearer 头不符：%q", got)
	}

	// 编辑时留空密码应保留原 Basic Auth 凭据
	oldBasic := &grafanaCredentials{Username: "ops", Password: "old-password"}
	_, _, _, basicCreds, err := validateGrafanaConnection(grafanaConnectionInput{
		Name:     "Basic Grafana",
		BaseURL:  "http://127.0.0.1:3000",
		AuthType: "basic",
		Username: "ops",
	}, oldBasic)
	if err != nil {
		t.Fatalf("留空密码保留旧凭据失败：%v", err)
	}
	reqBasic, _ := http.NewRequest("GET", "http://127.0.0.1:3000/api/datasources", nil)
	if err := applyGrafanaAuth(reqBasic, "basic", basicCreds); err != nil {
		t.Fatal(err)
	}
	wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("ops:old-password"))
	if got := reqBasic.Header.Get("Authorization"); got != wantBasic {
		t.Fatalf("Basic Auth 头不符：%q != %q", got, wantBasic)
	}

	// Cookie 认证与防头注入校验
	_, _, _, cookieCreds, err := validateGrafanaConnection(grafanaConnectionInput{
		Name:     "Cookie Grafana",
		BaseURL:  "http://127.0.0.1:3000",
		AuthType: "cookie",
		Cookie:   "grafana_session=abc123",
	}, nil)
	if err != nil {
		t.Fatalf("合法 Cookie 连接失败：%v", err)
	}
	reqCookie, _ := http.NewRequest("GET", "http://127.0.0.1:3000/api/datasources", nil)
	if err := applyGrafanaAuth(reqCookie, "cookie", cookieCreds); err != nil {
		t.Fatal(err)
	}
	if got := reqCookie.Header.Get("Cookie"); got != "grafana_session=abc123" {
		t.Fatalf("Cookie 头不符：%q", got)
	}

	if _, _, _, _, err := validateGrafanaConnection(grafanaConnectionInput{
		Name:     "Bad Cookie",
		BaseURL:  "http://127.0.0.1:3000",
		AuthType: "cookie",
		Cookie:   "a=1\r\nX-Injected: yes",
	}, nil); err == nil {
		t.Fatal("包含换行的 Cookie 应被拒绝")
	}
}

func TestGrafanaDefinitionValidateAndSchema(t *testing.T) {
	valid := Definition{
		Kind:           "grafana",
		Name:           "query_host_overview",
		Description:    "按主机 IP 查询指标与日志",
		TimeoutSeconds: 15,
		Grafana: &GrafanaConfig{
			ConnectionID: 1,
			FromRequired: false,
			DefaultFrom:  "now-1h",
			ToRequired:   true,
			Queries: []GrafanaQuery{
				{
					RefID:          "A",
					DatasourceUID:  "prom-uid",
					DatasourceType: "prometheus",
					Expr:           `up{instance=~"{{host_ip}}.*"}`,
				},
				{
					RefID:          "B",
					DatasourceUID:  "vmlogs-uid",
					DatasourceType: "victoriametrics-logs-datasource",
					Expr:           `host:"{{host_ip}}" AND _time:[{{from}}, {{to}}]`,
					MaxLines:       50,
				},
			},
		},
		Parameters: []Parameter{
			{Name: "host_ip", Description: "主机 IP", In: "query", Type: "string", Required: true, NonEmpty: true},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法多查询 Grafana 定义应通过校验：%v", err)
	}

	schema := valid.InputSchema()
	props := schema["properties"].(map[string]any)
	fromProp := props["from"].(map[string]any)
	if fromProp["default"] != "now-1h" {
		t.Fatalf("from 应包含 default=now-1h，实际：%#v", fromProp)
	}
	toProp := props["to"].(map[string]any)
	if toProp["pattern"] != `\S` {
		t.Fatalf("必填 to 应包含非空 pattern，实际：%#v", toProp)
	}
	required := schema["required"].([]string)
	if len(required) != 2 || required[0] != "to" || required[1] != "host_ip" {
		t.Fatalf("required 列表不符：%#v", required)
	}

	// 非法场景
	cases := []struct {
		name   string
		mutate func(*Definition)
	}{
		{"缺少默认 from", func(d *Definition) { d.Grafana.DefaultFrom = "" }},
		{"未配置占位符参数", func(d *Definition) { d.Grafana.Queries[0].Expr = `up{env="{{env}}"}` }},
		{"存在未引用死参数", func(d *Definition) {
			d.Parameters = append(d.Parameters, Parameter{Name: "unused", In: "query", Type: "string", Required: true})
		}},
		{"重复定义内置 from 参数", func(d *Definition) {
			d.Parameters = append(d.Parameters, Parameter{Name: "from", In: "query", Type: "string", Required: true})
		}},
		{"空占位符", func(d *Definition) { d.Grafana.Queries[0].Expr = `up{instance="{{}}"}` }},
		{"RefID 重复", func(d *Definition) { d.Grafana.Queries[1].RefID = "a" }},
		{"数组参数不支持", func(d *Definition) { d.Parameters[0].Type = "array" }},
		{"非法 dataType", func(d *Definition) { d.Grafana.Queries[0].DataType = "traces" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyDef := valid
			g := *valid.Grafana
			g.Queries = append([]GrafanaQuery{}, valid.Grafana.Queries...)
			copyDef.Grafana = &g
			copyDef.Parameters = append([]Parameter{}, valid.Parameters...)
			tc.mutate(&copyDef)
			if err := copyDef.Validate(); err == nil {
				t.Fatalf("场景 %s 应校验失败", tc.name)
			}
		})
	}
}

func TestBuildGrafanaRequestMultiDatasourceAndDefaults(t *testing.T) {
	def := Definition{
		Kind:           "grafana",
		Name:           "multi_ds_query",
		Description:    "多数据源联合查询",
		TimeoutSeconds: 15,
		Grafana: &GrafanaConfig{
			ConnectionID: 1,
			FromRequired: false,
			DefaultFrom:  "now-30m",
			ToRequired:   false,
			DefaultTo:    "now",
			Queries: []GrafanaQuery{
				{RefID: "prom", DatasourceUID: "u1", DatasourceType: "prometheus", Expr: `up{ip="{{host_ip}}"}`, QueryMode: "instant", Interval: "1m", MaxDataPoints: 500},
				{RefID: "loki", DatasourceUID: "u2", DatasourceType: "loki", Expr: `{host="{{host_ip}}"}`, MaxLines: 20},
				{RefID: "vmlogs", DatasourceUID: "u3", DatasourceType: "victoriametrics-logs-datasource", Expr: `host:"{{host_ip}}"`, QueryMode: "statsRange", Interval: "5m"},
				{RefID: "ch", DatasourceUID: "u4", DatasourceType: "grafana-clickhouse-datasource", Expr: `SELECT * FROM logs WHERE ip='{{host_ip}}' AND ts>='{{from}}'`},
				{RefID: "mysql", DatasourceUID: "u5", DatasourceType: "mysql", Expr: `SELECT * FROM hosts WHERE ip='{{host_ip}}'`},
				{RefID: "es", DatasourceUID: "u6", DatasourceType: "grafana-opensearch-datasource", Expr: `host.ip:"{{host_ip}}"`, MaxLines: 15, TimeField: "@timestamp"},
			},
		},
		Parameters: []Parameter{
			{Name: "host_ip", In: "query", Type: "string", Required: true, NonEmpty: true},
		},
	}
	conn := grafanaConnectionRecord{
		GrafanaConnection: GrafanaConnection{ID: 1, Name: "test", BaseURL: "https://grafana.example.com", AuthType: "bearer"},
		Credentials:       grafanaCredentials{Token: "tok-123"},
	}

	// 不传 from/to，自动使用默认值
	req, fromVal, toVal, err := def.buildGrafanaRequest(conn, map[string]any{"host_ip": "10.0.0.8"})
	if err != nil {
		t.Fatalf("构造 Grafana 请求失败：%v", err)
	}
	if fromVal != "now-30m" || toVal != "now" {
		t.Fatalf("默认时间未生效：from=%q to=%q", fromVal, toVal)
	}
	if req.URL.String() != "https://grafana.example.com/api/ds/query" {
		t.Fatalf("请求地址不符：%s", req.URL.String())
	}
	bodyBytes, _ := io.ReadAll(req.Body)
	var payload struct {
		From    string           `json:"from"`
		To      string           `json:"to"`
		Queries []map[string]any `json:"queries"`
	}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.From != "now-30m" || payload.To != "now" || len(payload.Queries) != 6 {
		t.Fatalf("payload 顶层不符：%#v", payload)
	}
	if payload.Queries[0]["expr"] != `up{ip="10.0.0.8"}` || payload.Queries[0]["instant"] != true || payload.Queries[0]["range"] != false {
		t.Fatalf("Prometheus 查询体不符：%#v", payload.Queries[0])
	}
	if payload.Queries[1]["queryType"] != "range" || payload.Queries[1]["maxLines"] != float64(20) {
		t.Fatalf("Loki 查询体不符：%#v", payload.Queries[1])
	}
	if payload.Queries[2]["queryType"] != "statsRange" || payload.Queries[2]["step"] != "5m" {
		t.Fatalf("VictoriaLogs 查询体不符：%#v", payload.Queries[2])
	}
	if payload.Queries[3]["queryType"] != "sql" || payload.Queries[3]["format"] != float64(1) || !strings.Contains(payload.Queries[3]["rawSql"].(string), "now-30m") {
		t.Fatalf("ClickHouse 查询体不符：%#v", payload.Queries[3])
	}
	if payload.Queries[4]["rawQuery"] != true || payload.Queries[4]["format"] != "table" {
		t.Fatalf("MySQL 查询体不符：%#v", payload.Queries[4])
	}
	if payload.Queries[5]["query"] != `host.ip:"10.0.0.8"` || payload.Queries[5]["timeField"] != "@timestamp" {
		t.Fatalf("OpenSearch 查询体不符：%#v", payload.Queries[5])
	}

	// 传入自定义 from/to 应覆盖默认值
	_, customFrom, customTo, err := def.buildGrafanaRequest(conn, map[string]any{
		"host_ip": "10.0.0.8",
		"from":    "2026-11-28T00:00:00Z",
		"to":      "2026-11-28T01:00:00Z",
	})
	if err != nil || customFrom != "2026-11-28T00:00:00Z" || customTo != "2026-11-28T01:00:00Z" {
		t.Fatalf("自定义时间未覆盖：%v from=%q to=%q", err, customFrom, customTo)
	}
}

func TestFormatGrafanaResponseAndOutputMapping(t *testing.T) {
	raw := []byte(`{
		"results": {
			"A": {
				"status": 200,
				"frames": [{
					"schema": {
						"name": "up",
						"refId": "A",
						"fields": [
							{"name": "Time", "type": "time"},
							{"name": "Value", "type": "number", "labels": {"instance": "10.0.0.8:9100"}}
						],
						"meta": {"notices": [{"severity": "info", "text": "sampled"}]}
					},
					"data": {
						"values": [
							[1700000000000, 1700000060000],
							[1, 0.5]
						]
					}
				}]
			},
			"B": {
				"status": 400,
				"error": "syntax error",
				"frames": []
			}
		}
	}`)
	queries := []GrafanaQuery{
		{RefID: "A", DatasourceType: "prometheus"},
		{RefID: "B", DatasourceType: "grafana-clickhouse-datasource", DataType: "logs"},
	}
	formatted, err := formatGrafanaResponse("now-1h", "now", queries, raw)
	if err != nil {
		t.Fatalf("整理 DataFrame 失败：%v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(formatted, &parsed); err != nil {
		t.Fatal(err)
	}
	results := parsed["results"].(map[string]any)
	aRes := results["A"].(map[string]any)
	if aRes["dataType"] != "metrics" {
		t.Fatalf("A 查询应自动标记为 metrics，实际：%#v", aRes["dataType"])
	}
	frames := aRes["frames"].([]any)
	frame0 := frames[0].(map[string]any)
	rows := frame0["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("应展开为 2 行，实际 %d", len(rows))
	}
	row0 := rows[0].(map[string]any)
	if row0["Time"] != "2023-11-14T22:13:20Z" || row0["Value"] != float64(1) {
		t.Fatalf("首行格式化不符：%#v", row0)
	}
	bRes := results["B"].(map[string]any)
	if bRes["dataType"] != "logs" {
		t.Fatalf("B 查询手动标注 logs 应保留，实际：%#v", bRes["dataType"])
	}
	if bRes["error"] != "syntax error" {
		t.Fatalf("B 查询错误未保留：%#v", bRes)
	}

	// 验证兼容现有 OutputFields 映射
	mapped, err := mapOutput([]OutputField{
		{Name: "values", Source: "response", Value: "/results/A/frames/0/rows/*/Value"},
		{Name: "instance", Source: "response", Value: "/results/A/frames/0/labels/instance"},
		{Name: "type", Source: "response", Value: "/results/A/dataType"},
	}, formatted)
	if err != nil {
		t.Fatalf("输出字段映射失败：%v", err)
	}
	if string(mapped) != `{"instance":"10.0.0.8:9100","type":"metrics","values":[1,0.5]}` {
		t.Fatalf("输出映射结果不符：%s", string(mapped))
	}
}
