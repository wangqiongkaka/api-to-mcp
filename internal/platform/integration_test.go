package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func jsonRequest(t *testing.T, c *http.Client, method, address string, input, out any) int {
	t.Helper()
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	r, err := http.NewRequest(method, address, body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		if err = json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

func TestPlatformIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("设置 TEST_DATABASE_URL 后运行 PostgreSQL 与 MCP 集成测试")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("集成测试须使用以 _test 结尾的独立数据库")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	namespace := fmt.Sprintf("integration_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+namespace); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := root.Exec(context.Background(), "DROP SCHEMA "+namespace+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	query := u.Query()
	query.Set("search_path", namespace)
	u.RawQuery = query.Encode()
	var guardedCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/guarded" {
			guardedCalls.Add(1)
		}
		if r.Header.Get("X-API-Key") != "private-upstream-secret" {
			http.Error(w, "missing secret", 401)
			return
		}
		if r.Method == "POST" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "invalid body", 400)
				return
			}
			writeJSON(w, 200, string(body))
			return
		}
		if r.URL.Query().Get("podIp") == "upstream-error" {
			http.Error(w, "upstream error", 502)
			return
		}
		if r.URL.Query().Get("podIp") == "missing-output" {
			writeJSON(w, 200, map[string]any{})
			return
		}
		writeJSON(w, 200, map[string]any{"podIp": r.URL.Query().Get("podIp"), "showAllPods": r.URL.Query().Get("showAllPods")})
	}))
	defer upstream.Close()
	cfg := Config{DatabaseURL: u.String(), EncryptionKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), AdminUsername: "ADMIN", AdminPassword: "integration-admin-password", AllowedOrigins: []string{upstream.URL}, WebDist: t.TempDir()}
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	client := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Timeout: 10 * time.Second}
	}
	alice, bob, admin := client(), client(), client()
	var aliceUser, bobUser User
	if code := jsonRequest(t, alice, "POST", server.URL+"/api/auth/register", map[string]any{"username": "alice", "password": "alice-password-123"}, &aliceUser); code != 200 || aliceUser.Role != "member" {
		t.Fatalf("注册失败：%d", code)
	}
	if code := jsonRequest(t, bob, "POST", server.URL+"/api/auth/register", map[string]any{"username": "bob", "password": "bob-password-123"}, &bobUser); code != 200 {
		t.Fatal(code)
	}
	if code := jsonRequest(t, admin, "POST", server.URL+"/api/auth/login", map[string]any{"username": "admin", "password": cfg.AdminPassword}, nil); code != 200 {
		t.Fatal(code)
	}
	if code := jsonRequest(t, client(), "GET", server.URL+"/api/tools", nil, nil); code != 401 {
		t.Fatal("未登录请求未被拦截")
	}
	if code := jsonRequest(t, client(), "POST", server.URL+"/api/auth/register", map[string]any{"username": "intruder", "password": "intruder-password-123", "role": "admin"}, nil); code != 400 {
		t.Fatal("注册请求可指定管理员权限")
	}
	if code := jsonRequest(t, client(), "POST", server.URL+"/api/auth/login", map[string]any{"username": "alice", "password": "wrong-password-123"}, nil); code != 401 {
		t.Fatal("错误密码被接受")
	}
	d := sampleDefinition()
	d.URL = upstream.URL + "/pods"
	d.Parameters = append(d.Parameters, Parameter{Name: "X-API-Key", In: "header", Type: "string", Fixed: true, Value: "private-upstream-secret"})
	var tool ToolRecord
	if code := jsonRequest(t, alice, "POST", server.URL+"/api/tools", d, &tool); code != 201 || tool.OwnerID != aliceUser.ID {
		t.Fatalf("创建接口失败：%d", code)
	}
	address := fmt.Sprintf("%s/api/tools/%d", server.URL, tool.ID)
	var ownTools []ToolRecord
	if code := jsonRequest(t, bob, "GET", server.URL+"/api/tools", nil, &ownTools); code != 200 || len(ownTools) != 0 {
		t.Fatal("泄露其他用户接口")
	}
	for _, op := range []struct {
		method, path string
		body         any
	}{{"PUT", "", d}, {"DELETE", "", nil}, {"POST", "/publish", map[string]bool{"published": true}}, {"POST", "/call", map[string]any{"arguments": map[string]any{"podIp": "x"}}}, {"GET", "/export?format=openapi", nil}} {
		if code := jsonRequest(t, bob, op.method, address+op.path, op.body, nil); code != 404 {
			t.Fatalf("越权请求未被拦截：%s %s %d", op.method, op.path, code)
		}
	}
	if code := jsonRequest(t, bob, "GET", server.URL+"/api/users", nil, nil); code != 403 {
		t.Fatal("成员可以管理用户")
	}
	if code := jsonRequest(t, alice, "POST", server.URL+"/api/tools", d, nil); code != 409 {
		t.Fatal("同用户同名工具未被拒绝")
	}
	var encrypted []byte
	if err = a.db.QueryRow(ctx, "SELECT definition FROM tools WHERE id=$1", tool.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("private-upstream-secret")) {
		t.Fatal("上游凭据明文落库")
	}
	for _, format := range []string{"openapi", "higress"} {
		if code := jsonRequest(t, alice, "GET", address+"/export?format="+format, nil, nil); code != 200 {
			t.Fatalf("导出失败：%s", format)
		}
	}
	var call CallResult
	if code := jsonRequest(t, alice, "POST", address+"/call", map[string]any{"arguments": map[string]any{"podIp": "10.60.144.183"}}, &call); code != 200 || call.StatusCode != 200 {
		t.Fatal("试调用失败")
	}
	if code := jsonRequest(t, alice, "POST", address+"/call", map[string]any{"arguments": map[string]any{"podIp": "x", "showAllPods": true}}, nil); code != 400 {
		t.Fatal("固定参数可被覆盖")
	}
	if code := jsonRequest(t, alice, "POST", address+"/call", map[string]any{"arguments": nil}, nil); code != 400 {
		t.Fatal("空值被当作参数对象接受")
	}
	var token struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if code := jsonRequest(t, alice, "POST", server.URL+"/api/tokens", map[string]string{"name": "test-client"}, &token); code != 201 {
		t.Fatal(code)
	}
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		c := mcp.NewClient(&mcp.Implementation{Name: "integration-client", Version: "1.0.0"}, nil)
		s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token}, Timeout: 10 * time.Second}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	session := connect(token.Token)
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 0 {
		t.Fatalf("草稿出现在 MCP 工具列表：%v", err)
	}
	if code := jsonRequest(t, alice, "POST", address+"/publish", map[string]bool{"published": true}, nil); code != 200 {
		t.Fatal(code)
	}
	list, err = session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != d.Name {
		t.Fatalf("MCP 工具发现失败：%v", err)
	}
	if code := jsonRequest(t, alice, "PUT", address, d, nil); code != 409 {
		t.Fatal("已发布接口可被直接修改")
	}
	mcpResult, err := session.CallTool(ctx, &mcp.CallToolParams{Name: d.Name, Arguments: map[string]any{"podIp": "10.60.144.183"}})
	if err != nil || mcpResult.IsError {
		t.Fatalf("MCP 调用失败：%v", err)
	}
	data, _ := json.Marshal(mcpResult.StructuredContent)
	var mcpResponse CallResult
	if err = json.Unmarshal(data, &mcpResponse); err != nil {
		t.Fatal(err)
	}
	var echo map[string]string
	if err = json.Unmarshal([]byte(mcpResponse.Body), &echo); err != nil || echo["showAllPods"] != "false" || echo["podIp"] != "10.60.144.183" {
		t.Fatal("MCP 请求未保留固定值或动态参数")
	}
	mcpResult, err = session.CallTool(ctx, &mcp.CallToolParams{Name: d.Name, Arguments: map[string]any{"podIp": "x", "showAllPods": true}})
	if err != nil || !mcpResult.IsError {
		t.Fatal("MCP 固定值覆盖未被拒绝")
	}
	mcpResult, err = session.CallTool(ctx, &mcp.CallToolParams{Name: d.Name, Arguments: json.RawMessage(`null`)})
	if err != nil || !mcpResult.IsError {
		t.Fatal("MCP 接受了 null 参数对象")
	}
	var bobToken struct {
		Token string `json:"token"`
	}
	jsonRequest(t, bob, "POST", server.URL+"/api/tokens", map[string]string{"name": "bob-client"}, &bobToken)
	bobSession := connect(bobToken.Token)
	list, err = bobSession.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 0 {
		t.Fatal("MCP 令牌泄露其他用户工具")
	}
	if code := jsonRequest(t, alice, "POST", address+"/publish", map[string]bool{"published": false}, nil); code != 200 {
		t.Fatal(code)
	}
	list, err = session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 0 {
		t.Fatal("工具停用未生效")
	}
	mapped := d
	mapped.OutputFields = []OutputField{
		{Name: "ip", Source: "response", Value: "/podIp"},
		{Name: "source", Source: "fixed", Value: `"inventory"`},
	}
	if code := jsonRequest(t, alice, "PUT", address, mapped, nil); code != 200 {
		t.Fatalf("输出配置保存失败：%d", code)
	}
	var mappedTools []ToolRecord
	if code := jsonRequest(t, alice, "GET", server.URL+"/api/tools", nil, &mappedTools); code != 200 || len(mappedTools) != 1 || len(mappedTools[0].OutputFields) != 2 || mappedTools[0].OutputFields[0] != mapped.OutputFields[0] {
		t.Fatalf("输出配置读取失败：%d，%+v", code, mappedTools)
	}
	if code := jsonRequest(t, alice, "POST", address+"/publish", map[string]bool{"published": true}, nil); code != 200 {
		t.Fatalf("配置输出字段的工具发布失败：%d", code)
	}
	for _, input := range []struct {
		ip     string
		status int
		want   string
		mapped bool
	}{
		{"10.60.144.183", 200, `{"ip":"10.60.144.183","source":"inventory"}`, true},
		{"missing-output", 400, `/podIp`, false},
		{"upstream-error", 502, "upstream error", false},
	} {
		args := map[string]any{"podIp": input.ip}
		var direct struct {
			CallResult
			Error string `json:"error"`
		}
		status := jsonRequest(t, alice, "POST", address+"/call", map[string]any{"arguments": args}, &direct)
		if input.status == 400 {
			if status != 400 || !strings.Contains(direct.Error, input.want) {
				t.Fatalf("试调用映射错误未返回：%d，%+v", status, direct)
			}
		} else if status != 200 || direct.StatusCode != input.status || direct.OutputMapped != input.mapped || !strings.Contains(direct.Body, input.want) {
			t.Fatalf("试调用输出错误：%d，%+v", status, direct)
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mapped.Name, Arguments: args})
		if err != nil || result.IsError == input.mapped || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, input.want) {
			t.Fatalf("MCP 输出错误：%v，%v", result, err)
		}
		if input.mapped {
			data, err := json.Marshal(result.StructuredContent)
			if err != nil || string(data) != direct.Body {
				t.Fatalf("MCP 与试调用的 JSON 不一致：%s，%s，%v", data, direct.Body, err)
			}
		}
	}
	whole := d
	whole.Name, whole.Method, whole.WholeBody = "queryWithBody", "POST", true
	var wholeTool ToolRecord
	if code := jsonRequest(t, alice, "POST", server.URL+"/api/tools", whole, &wholeTool); code != 201 || !wholeTool.WholeBody {
		t.Fatalf("完整请求体配置未保存：%d", code)
	}
	wholeAddress := fmt.Sprintf("%s/api/tools/%d", server.URL, wholeTool.ID)
	if code := jsonRequest(t, alice, "POST", wholeAddress+"/publish", map[string]bool{"published": true}, nil); code != 200 {
		t.Fatalf("完整请求体工具发布失败：%d", code)
	}
	for _, body := range []string{"", "null", `{"id":9007199254740993,"queryParams":[]}`} {
		args := map[string]any{"podIp": "x"}
		if body != "" {
			args["body"] = json.RawMessage(body)
		}
		want, _ := json.Marshal(body)
		var direct CallResult
		if code := jsonRequest(t, alice, "POST", wholeAddress+"/call", map[string]any{"arguments": args}, &direct); code != 200 || strings.TrimSpace(direct.Body) != string(want) {
			t.Fatalf("完整请求体试调用转发错误：%d，%q", code, direct.Body)
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: whole.Name, Arguments: args})
		if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, string(want)) {
			t.Fatalf("完整请求体 MCP 调用转发错误：%v，%v", result, err)
		}
	}
	other, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := other.getTool(ctx, tool.ID, aliceUser, false)
	other.Close()
	if err != nil || persisted.Name != d.Name {
		t.Fatal("数据库配置未持久化")
	}
	t.Run("管理员共享接口与成员自有接口隔离", func(t *testing.T) {
		var shared, own ToolRecord
		check := func(client *http.Client, method, path string, body, out any, want int) {
			t.Helper()
			if got := jsonRequest(t, client, method, server.URL+path, body, out); got != want {
				t.Fatalf("%s %s 返回 %d，预期 %d", method, path, got, want)
			}
		}
		check(admin, "POST", "/api/tools", mapped, &shared, 201)
		check(bob, "POST", "/api/tools", d, &own, 201)
		if shared.Public || own.Public {
			t.Fatal("新建接口默认公开")
		}
		sharedPath := fmt.Sprintf("/api/tools/%d", shared.ID)
		ownPath := fmt.Sprintf("/api/tools/%d", own.ID)
		share := map[string]bool{"public": true}
		publish := map[string]bool{"published": true}
		args := map[string]any{"podIp": "192.0.2.17"}
		call := map[string]any{"arguments": args, "raw": true}
		publicName := fmt.Sprintf("%s.public.%d", mapped.Name, shared.ID)
		check(bob, "PATCH", ownPath+"/visibility", share, nil, 403)
		check(admin, "PATCH", ownPath+"/visibility", share, nil, 404)
		check(admin, "PATCH", sharedPath+"/visibility", map[string]bool{}, nil, 400)
		check(admin, "PATCH", sharedPath+"/visibility", share, nil, 200)
		check(bob, "POST", ownPath+"/publish", publish, nil, 200)

		checkVisibility := func(visible bool) {
			t.Helper()
			var items []ToolRecord
			check(bob, "GET", "/api/tools", nil, &items, 200)
			want := 1
			if visible {
				want++
			}
			if len(items) != want || !slices.ContainsFunc(items, func(item ToolRecord) bool { return item.ID == own.ID && item.Name == d.Name }) {
				t.Fatalf("成员的接口库包含意外记录：%+v", items)
			}
			for _, item := range items {
				if item.ID == own.ID {
					continue
				}
				if item.ID != shared.ID || item.Name != publicName || !item.Public || !item.Published || item.URL != "" || item.BodyTemplate != "" || len(item.OutputFields) != 0 || len(item.Parameters) != 1 || item.Parameters[0].Name != "podIp" || item.Parameters[0].Fixed || item.Parameters[0].Value != "" {
					t.Fatalf("公共接口信息未正确裁剪：%+v", item)
				}
			}
			result, err := bobSession.ListTools(ctx, nil)
			if err != nil || len(result.Tools) != want || !slices.ContainsFunc(result.Tools, func(tool *mcp.Tool) bool { return tool.Name == d.Name }) || visible != slices.ContainsFunc(result.Tools, func(tool *mcp.Tool) bool { return tool.Name == publicName }) {
				t.Fatalf("MCP 发现结果不符合共享状态 %t：%+v，%v", visible, result, err)
			}
			if !visible {
				check(bob, "POST", sharedPath+"/call", call, nil, 404)
				result, err := bobSession.CallTool(ctx, &mcp.CallToolParams{Name: publicName, Arguments: args})
				if err == nil && (result == nil || !result.IsError) {
					t.Fatal("不可见的公共工具仍可通过 MCP 调用")
				}
			}
		}
		checkVisibility(false)
		check(admin, "POST", sharedPath+"/publish", publish, nil, 200)
		checkVisibility(true)
		check(bob, "PUT", sharedPath, mapped, nil, 404)
		check(bob, "DELETE", sharedPath, nil, nil, 404)
		check(bob, "POST", sharedPath+"/publish", publish, nil, 404)
		check(bob, "PATCH", sharedPath+"/visibility", share, nil, 403)
		for _, format := range []string{"openapi", "higress"} {
			check(bob, "GET", sharedPath+"/export?format="+format, nil, nil, 404)
		}
		check(client(), "POST", sharedPath+"/call", call, nil, 401)
		var response CallResult
		check(bob, "POST", sharedPath+"/call", call, &response, 200)
		if !response.OutputMapped || response.Body != `{"ip":"192.0.2.17","source":"inventory"}` {
			t.Fatalf("公共试调用绕过输出字段：%+v", response)
		}
		for name, expectText := range map[string]string{d.Name: `"podIp":"192.0.2.17"`, publicName: response.Body} {
			result, err := bobSession.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, expectText) {
				t.Fatalf("同名工具 %q 调用结果错误：%+v，%v", name, result, err)
			}
		}
		check(admin, "POST", sharedPath+"/publish", map[string]bool{"published": false}, nil, 200)
		checkVisibility(false)
		check(admin, "POST", sharedPath+"/publish", publish, nil, 200)
		checkVisibility(true)
		check(admin, "PATCH", sharedPath+"/visibility", map[string]bool{"public": false}, nil, 200)
		checkVisibility(false)
	})
	t.Run("试调用和 MCP 都在转发前检查筛选参数", func(t *testing.T) {
		guarded := d
		guarded.Name, guarded.Method, guarded.URL = "guardedQuery", "POST", upstream.URL+"/guarded"
		guarded.BodyTemplate = `{"queryParams":[{"ip":"{{host_ip}}"},{"owner":"{{admin}}"}]}`
		guarded.Parameters = []Parameter{
			{Name: "host_ip", In: "body", Type: "array", QueryFilter: true},
			{Name: "admin", In: "body", Type: "array", QueryFilter: true},
			{Name: "limit", In: "query", Type: "integer"},
			d.Parameters[len(d.Parameters)-1],
		}
		var record ToolRecord
		if code := jsonRequest(t, alice, "POST", server.URL+"/api/tools", guarded, &record); code != 201 {
			t.Fatal(code)
		}
		path := fmt.Sprintf("%s/api/tools/%d", server.URL, record.ID)
		if code := jsonRequest(t, alice, "POST", path+"/publish", map[string]bool{"published": true}, nil); code != 200 {
			t.Fatal(code)
		}
		const message = "请至少填写一个筛选参数：host_ip、admin"
		args := map[string]any{"limit": 1, "host_ip": []any{""}, "admin": []any{" "}}
		var rejection struct{ Error string }
		if code := jsonRequest(t, alice, "POST", path+"/call", map[string]any{"arguments": args, "raw": true}, &rejection); code != 400 || rejection.Error != message {
			t.Fatalf("试调用筛选错误：%d，%+v", code, rejection)
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: guarded.Name, Arguments: args})
		if err != nil || !result.IsError || result.Content[0].(*mcp.TextContent).Text != message || guardedCalls.Load() != 0 {
			t.Fatalf("MCP 筛选错误：%+v，%v，上游收到 %d 次调用", result, err, guardedCalls.Load())
		}
		args = map[string]any{"host_ip": []any{"192.0.2.1"}, "admin": []any{""}}
		var call CallResult
		want, _ := json.Marshal(`{"queryParams":[{"ip":["192.0.2.1"]}]}`)
		if code := jsonRequest(t, alice, "POST", path+"/call", map[string]any{"arguments": args}, &call); code != 200 || strings.TrimSpace(call.Body) != string(want) {
			t.Fatalf("试调用应只转发 IP 条件：%d，响应 %s", code, call.Body)
		}
		args = map[string]any{"host_ip": []any{""}, "admin": []any{"owner"}}
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: guarded.Name, Arguments: args})
		want, _ = json.Marshal(`{"queryParams":[{"owner":["owner"]}]}`)
		if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, string(want)) || guardedCalls.Load() != 2 {
			t.Fatalf("MCP 应只转发负责人条件：%+v，%v，上游收到 %d 次调用", result, err, guardedCalls.Load())
		}
	})
	if code := jsonRequest(t, alice, "DELETE", fmt.Sprintf("%s/api/tokens/%d", server.URL, token.ID), nil, nil); code != 200 {
		t.Fatal(code)
	}
	if _, err = session.ListTools(ctx, nil); err == nil {
		t.Fatal("被撤销的 MCP 令牌仍然有效")
	}
	jsonRequest(t, alice, "POST", server.URL+"/api/tokens", map[string]string{"name": "disable-test"}, &token)
	disabledSession := connect(token.Token)
	if code := jsonRequest(t, admin, "PATCH", fmt.Sprintf("%s/api/users/%d", server.URL, aliceUser.ID), map[string]bool{"disabled": true}, nil); code != 200 {
		t.Fatal(code)
	}
	if code := jsonRequest(t, alice, "GET", server.URL+"/api/me", nil, nil); code != 401 {
		t.Fatal("停用用户的会话仍有效")
	}
	if _, err = disabledSession.ListTools(ctx, nil); err == nil {
		t.Fatal("停用用户的 MCP 令牌仍有效")
	}
	jsonRequest(t, admin, "PATCH", fmt.Sprintf("%s/api/users/%d", server.URL, aliceUser.ID), map[string]bool{"disabled": false}, nil)
	if _, err = disabledSession.ListTools(ctx, nil); err == nil {
		t.Fatal("已撤销令牌在账户启用后恢复了")
	}
	if code := jsonRequest(t, admin, "PATCH", server.URL+"/api/users/1", map[string]bool{"disabled": true}, nil); code != 400 {
		t.Fatal("受保护管理员可被停用")
	}
	if code := jsonRequest(t, bob, "POST", server.URL+"/api/auth/logout", map[string]any{}, nil); code != 200 {
		t.Fatal(code)
	}
	if code := jsonRequest(t, bob, "GET", server.URL+"/api/me", nil, nil); code != 401 {
		t.Fatal("退出登录未撤销会话")
	}
}
