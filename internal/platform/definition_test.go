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
