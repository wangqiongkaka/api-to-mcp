package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/higress-group/openapi-to-mcpserver/pkg/models"
	"github.com/higress-group/openapi-to-mcpserver/pkg/parser"
	"gopkg.in/yaml.v3"
)

func sampleDefinition() Definition {
	return Definition{Name: "getPodInstance", Description: "查询 Pod 实例", URL: "http://10.12.72.42:9007/ws/aquaman/kubernetes/pods/getPodInstance", Method: "GET", TimeoutSeconds: 5,
		Parameters: []Parameter{{Name: "showAllPods", In: "query", Type: "boolean", Fixed: true, Value: "false"}, {Name: "podIp", In: "query", Type: "string", Required: true}}}
}

func TestBuildRequest(t *testing.T) {
	d := sampleDefinition()
	r, err := d.BuildRequest(map[string]any{"podIp": "10.60.144.183&admin=true"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "GET" || r.URL.Query().Get("showAllPods") != "false" || r.URL.Query().Get("podIp") != "10.60.144.183&admin=true" || r.URL.Query().Has("admin") {
		t.Fatalf("查询参数没有正确映射：%s", r.URL)
	}
	for _, args := range []map[string]any{{}, {"podIp": false}, {"podIp": "x", "showAllPods": true}, {"podIp": "x", "unknown": "y"}} {
		if _, err = d.BuildRequest(args); err == nil {
			t.Fatalf("无效参数被接受：%v", args)
		}
	}
	schema := d.InputSchema()
	if len(schema["properties"].(map[string]any)) != 1 {
		t.Fatal("固定参数暴露给了客户端")
	}
}

func TestBuildRequestRequiresQueryFilter(t *testing.T) {
	d := sampleDefinition()
	d.Method = "POST"
	d.Parameters = []Parameter{
		{Name: "host_ip", In: "body", Type: "array", QueryFilter: true},
		{Name: "admin", In: "body", Type: "array", QueryFilter: true, ItemArrayField: "value"},
		{Name: "limit", In: "query", Type: "integer"},
		{Name: "auth", In: "header", Type: "string", Fixed: true, Value: "secret"},
		{Name: "active", In: "query", Type: "boolean", QueryFilter: true},
	}
	d.BodyTemplate = `{"filters":[{"ip":"{{host_ip}}"},{"byAdmin":"{{admin}}"}]}`
	const message = "请至少填写一个筛选参数：host_ip、admin、active"
	for _, tc := range []struct {
		args string
		pass bool
	}{
		{`{}`, false},
		{`{"limit":1}`, false},
		{`{"host_ip":[],"limit":1}`, false},
		{`{"host_ip":[null," ",[]]}`, false},
		{`{"admin":[{"field":"name","value":[" "]}]}`, false},
		{`{"host_ip":["192.0.2.1"]}`, true},
		{`{"host_ip":[],"admin":[{"value":["owner"]}]}`, true},
		{`{"host_ip":["192.0.2.1"],"admin":[{"value":["owner"]}]}`, true},
		{`{"active":false}`, true},
	} {
		args, err := d.decodeArguments([]byte(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if r, err := d.BuildRequest(args); tc.pass {
			if err != nil {
				t.Errorf("调用 %s 失败：%v", tc.args, err)
			} else {
				r.Body.Close()
			}
		} else if err == nil || err.Error() != message || r != nil {
			t.Errorf("调用 %s 应在构造请求前拒绝：请求 %v，错误 %v", tc.args, r, err)
		}
	}
	if schema := d.InputSchema(); schema["description"] != message || len(schema["required"].([]string)) != 0 {
		t.Fatalf("筛选参数输入说明错误：%v", schema)
	}
	d.BodyTemplate = `{"filters":[{"ip":"{{host_ip}}","byAdmin":"{{admin}}"}]}`
	if _, err := d.BuildRequest(map[string]any{"host_ip": []any{"192.0.2.1"}}); err == nil || err.Error() != message {
		t.Fatalf("被模板省略的筛选条件应拒绝：%v", err)
	}
	d.Parameters[3].QueryFilter = true
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "仅客户端填写的参数可设为筛选参数") {
		t.Fatalf("固定参数不可设为筛选参数：%v", err)
	}
}

func TestPathHeaderAndBody(t *testing.T) {
	d := Definition{Name: "updateUser", Description: "更新用户", Method: "PATCH", URL: "https://example.com/users/{id}", TimeoutSeconds: 5, Parameters: []Parameter{
		{Name: "id", In: "path", Type: "string", Required: true},
		{Name: "Authorization", In: "header", Type: "string", Fixed: true, Value: "Bearer private-key"},
		{Name: "profile", In: "body", Type: "object", Required: true},
		{Name: "enabled", In: "body", Type: "boolean", Fixed: true, Value: "false"},
	}}
	r, err := d.BuildRequest(map[string]any{"id": "a/b ?", "profile": map[string]any{"name": "小明"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.EscapedPath() != "/users/a%2Fb%20%3F" || r.Header.Get("Authorization") != "Bearer private-key" || r.Header.Get("Content-Type") != "application/json" {
		t.Fatal("路径或请求头映射错误")
	}
	var body map[string]any
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil || body["enabled"] != false || body["profile"].(map[string]any)["name"] != "小明" {
		t.Fatalf("JSON 请求体错误：%v", err)
	}
	if _, err = d.BuildRequest(map[string]any{"id": "..", "profile": map[string]any{}}); err == nil {
		t.Fatal("接受了目录跳转参数")
	}
}

func TestRequiredArrayWithAtLeastOneItem(t *testing.T) {
	d := sampleDefinition()
	d.Method = "POST"
	d.Parameters = []Parameter{{Name: "queryParams", In: "body", Type: "array", Required: true, NonEmpty: true}}
	for _, args := range []map[string]any{{}, {"queryParams": []any{}}, {"queryParams": nil}} {
		if _, err := d.BuildRequest(args); err == nil {
			t.Fatalf("必填数组必须至少有一项，收到：%v", args)
		}
	}
	if _, err := d.BuildRequest(map[string]any{"queryParams": []any{"value"}}); err != nil {
		t.Fatal(err)
	}
	queryParams := d.InputSchema()["properties"].(map[string]any)["queryParams"].(map[string]any)
	if queryParams["minItems"] != 1 {
		t.Fatal("MCP 客户端未收到数组最少项数约束")
	}
	data, err := d.OpenAPI()
	if err != nil || !strings.Contains(string(data), `"minItems": 1`) {
		t.Fatalf("OpenAPI 未输出数组最少项数约束：%s，%v", data, err)
	}
	d.Parameters[0].NonEmpty = false
	if _, err := d.BuildRequest(map[string]any{"queryParams": []any{}}); err != nil {
		t.Fatalf("未启用至少一项的接口应继续接受空数组：%v", err)
	}
}

func TestArrayItemRequiresNonEmptyArrayField(t *testing.T) {
	d := sampleDefinition()
	d.Method = "POST"
	d.Parameters = []Parameter{{Name: "queryParams", In: "body", Type: "array", Required: true, NonEmpty: true, ItemArrayField: "value"}}
	valid := map[string]any{"uniqueCode": "field-id", "value": []any{"10.12.78.132"}}
	for _, item := range []any{
		"10.12.78.132",
		map[string]any{"uniqueCode": "field-id"},
		map[string]any{"value": nil},
		map[string]any{"value": "10.12.78.132"},
		map[string]any{"value": []any{}},
	} {
		args := map[string]any{"queryParams": []any{valid, item}}
		if _, err := d.BuildRequest(args); err == nil || !strings.Contains(err.Error(), "queryParams[1].value") {
			t.Fatalf("应定位第二项中无效的 value：%v，%v", item, err)
		}
	}
	if _, err := d.BuildRequest(map[string]any{"queryParams": []any{valid}}); err != nil {
		t.Fatal(err)
	}
	queryParams := d.InputSchema()["properties"].(map[string]any)["queryParams"].(map[string]any)
	items := queryParams["items"].(map[string]any)
	field := items["properties"].(map[string]any)["value"].(map[string]any)
	if items["type"] != "object" || items["required"].([]string)[0] != "value" || field["type"] != "array" || field["minItems"] != 1 {
		t.Fatalf("MCP Schema 未声明每个元素中的非空数组字段：%v", items)
	}
	data, err := d.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	p := parser.NewParser()
	p.SetValidation(true)
	if err := p.Parse(data); err != nil {
		t.Fatalf("OpenAPI 导出未通过验证：%v", err)
	}
}

func TestDefinitionRejectsInvalidInputs(t *testing.T) {
	cases := map[string]func(*Definition){
		"非 HTTP 地址": func(d *Definition) { d.URL = "file:///etc/passwd" },
		"嵌入凭据":      func(d *Definition) { d.URL = "http://user:pass@example.com/" },
		"非法方法":      func(d *Definition) { d.Method = "CONNECT" },
		"重复参数":      func(d *Definition) { d.Parameters = append(d.Parameters, d.Parameters[0]) },
		"固定类型错误":    func(d *Definition) { d.Parameters[0].Value = "no" },
		"路径未定义":     func(d *Definition) { d.URL = "https://example.com/{missing}" },
		"连接控制头": func(d *Definition) {
			d.Parameters = append(d.Parameters, Parameter{Name: "Host", In: "header", Type: "string", Fixed: true, Value: "internal"})
		},
		"请求头换行": func(d *Definition) {
			d.Parameters = append(d.Parameters, Parameter{Name: "X-Key", In: "header", Type: "string", Fixed: true, Value: "x\r\nInjected: y"})
		},
		"非法超时": func(d *Definition) { d.TimeoutSeconds = 0 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			d := sampleDefinition()
			change(&d)
			if err := d.Validate(); err == nil {
				t.Fatal("无效定义被接受")
			}
		})
	}
}

func TestWholeBodyLetsCallerChooseJSONOrNoBody(t *testing.T) {
	d := sampleDefinition()
	d.Method = "POST"
	d.WholeBody = true
	for _, want := range []string{"", "{}", "[]", "null", `{"id":9007199254740993,"queryParams":[]}`} {
		raw := `{"podIp":"example"}`
		if want != "" {
			raw = `{"podIp":"example","body":` + want + "}"
		}
		args, err := d.decodeArguments([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		r, err := d.BuildRequest(args)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || string(body) != want || (r.Header.Get("Content-Type") == "application/json") != (want != "") {
			t.Fatalf("请求体或内容类型不符：%q，期望 %q，错误 %v", body, want, err)
		}
		if r.URL.Query().Get("podIp") != "example" || r.URL.Query().Get("showAllPods") != "false" {
			t.Fatal("完整请求体影响了其他参数映射")
		}
	}
	d.Parameters = append(d.Parameters, Parameter{Name: "queryParams", In: "body", Type: "array"})
	if err := d.Validate(); err == nil {
		t.Fatal("同一接口同时启用了完整请求体和字段映射")
	}
}

func TestBodyTemplateMapsClientValueIntoUpstreamArray(t *testing.T) {
	d := sampleDefinition()
	d.Method = "POST"
	d.Parameters = []Parameter{
		{Name: "value", In: "body", Type: "string", Required: true},
		{Name: "Cookie", In: "header", Type: "string", Fixed: true, Value: "private"},
	}
	d.BodyTemplate = `{"queryParams":[{"uniqueCode":"field-id","value":["{{value}}"]}]}`
	const ip = "10.12.78.132"
	r, err := d.BuildRequest(map[string]any{"value": ip})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil || string(body) != `{"queryParams":[{"uniqueCode":"field-id","value":["10.12.78.132"]}]}` || r.Header.Get("Cookie") != "private" {
		t.Fatalf("上游请求体或固定请求头不符：%s，%v", body, err)
	}
	properties := d.InputSchema()["properties"].(map[string]any)
	if len(properties) != 1 || properties["value"] == nil {
		t.Fatalf("客户端应只填写 value：%v", properties)
	}
	if _, err := d.BuildRequest(nil); err == nil {
		t.Fatal("模板接受了缺失的必填参数")
	}
	for _, bad := range []string{`{bad}`, `{"queryParams":[]}`, `{"queryParams":["{{Cookie}}"]}`} {
		d.BodyTemplate = bad
		if err := d.Validate(); err == nil {
			t.Fatalf("错误的模板配置被保存：%s", bad)
		}
	}
}

func TestBodyTemplateOmitsUnprovidedOptionalArray(t *testing.T) {
	d := sampleDefinition()
	d.Method = "POST"
	d.Parameters = []Parameter{
		{Name: "ips", In: "body", Type: "array", NonEmpty: true},
		{Name: "limit", In: "body", Type: "integer", Required: true},
	}
	d.BodyTemplate = `{
		"ips": "{{ips}}",
		"queryParams": [
			{"uniqueCode":"ip","value":"{{ips}}"},
			{"uniqueCode":"wrapped","value":["{{ips}}"]},
			{"uniqueCode":"limit","value":"{{limit}}"}
		],
		"values": [null, "{{ips}}"]
	}`
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if required := d.InputSchema()["required"].([]string); len(required) != 1 || required[0] != "limit" {
		t.Fatalf("必填参数不符：%v", required)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"省略数组", map[string]any{"limit": float64(7)}, `{"queryParams":[{"uniqueCode":"limit","value":7}],"values":[null]}`},
		{"传入数组", map[string]any{"limit": float64(7), "ips": []any{"example"}}, `{"ips":["example"],"queryParams":[{"uniqueCode":"ip","value":["example"]},{"uniqueCode":"wrapped","value":[["example"]]},{"uniqueCode":"limit","value":7}],"values":[null,["example"]]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := d.BuildRequest(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != tc.want {
				t.Fatalf("请求体 = %s，期望 %s，错误 %v", body, tc.want, err)
			}
		})
	}
	for _, invalid := range []any{nil, []any{}} {
		if _, err := d.BuildRequest(map[string]any{"limit": float64(7), "ips": invalid}); err == nil {
			t.Fatalf("接受了无效数组：%v", invalid)
		}
	}
}

func TestIntegerPrecisionBoundary(t *testing.T) {
	d := sampleDefinition()
	d.Parameters[1].Type = "integer"
	if _, err := d.BuildRequest(map[string]any{"podIp": float64(9007199254740991)}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []float64{9007199254740992, -9007199254740992, 1.5} {
		if _, err := d.BuildRequest(map[string]any{"podIp": value}); err == nil {
			t.Fatal("可能损失精度或非整数的值被接受")
		}
	}
}

func TestBodyContentTypeCannotBeOverridden(t *testing.T) {
	d := sampleDefinition()
	d.Method = "POST"
	d.Parameters = append(d.Parameters, Parameter{Name: "data", In: "body", Type: "object"}, Parameter{Name: "Content-Type", In: "header", Type: "string", Fixed: true, Value: "text/plain"})
	if err := d.Validate(); err == nil {
		t.Fatal("请求体格式与 Content-Type 冲突未被拒绝")
	}
}

func TestExportsReuseConverter(t *testing.T) {
	d := sampleDefinition()
	data, err := d.OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	p := parser.NewParser()
	p.SetValidation(true)
	if err = p.Parse(data); err != nil {
		t.Fatal(err)
	}
	data, err = d.Higress()
	if err != nil {
		t.Fatal(err)
	}
	var cfg models.MCPConfig
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tools) != 1 || len(cfg.Tools[0].Args) != 1 || cfg.Tools[0].Args[0].Name != "podIp" {
		t.Fatal("导出的动态参数不正确")
	}
	u, err := url.Parse(cfg.Tools[0].RequestTemplate.URL)
	if err != nil || u.Query().Get("showAllPods") != "false" {
		t.Fatal("固定参数未写入导出地址")
	}
	d.URL = "https://example.com/{id}"
	d.Parameters = []Parameter{{Name: "id", In: "path", Type: "string", Fixed: true, Value: "a/b"}}
	data, err = d.Higress()
	if err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	u, err = url.Parse(cfg.Tools[0].RequestTemplate.URL)
	if err != nil || u.EscapedPath() != "/a%2Fb" {
		t.Fatalf("固定路径转义错误：%s", data)
	}
}

func TestUpstreamBoundaries(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("不应跟随重定向") }))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, target.URL, 302)
		case "/large":
			io.WriteString(w, strings.Repeat("x", (2<<20)+1))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		default:
			io.WriteString(w, "ok")
		}
	}))
	defer upstream.Close()
	a := &App{allowed: map[string]bool{upstream.URL: true}, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	d := Definition{Name: "test", Description: "测试", URL: upstream.URL, Method: "GET", TimeoutSeconds: 1}
	if _, err := a.execute(context.Background(), d, nil); err != nil {
		t.Fatal(err)
	}
	d.URL = target.URL
	if _, err := a.execute(context.Background(), d, nil); err == nil {
		t.Fatal("未批准地址被接受")
	}
	d.URL = upstream.URL + "/redirect"
	result, err := a.execute(context.Background(), d, nil)
	if err != nil || result.StatusCode != 302 {
		t.Fatal("重定向边界错误")
	}
	d.URL = upstream.URL + "/large"
	if _, err = a.execute(context.Background(), d, nil); err == nil {
		t.Fatal("超大响应被接受")
	}
	d.URL = upstream.URL + "/slow"
	if _, err = a.execute(context.Background(), d, nil); err == nil {
		t.Fatal("超时请求被接受")
	}
}
