package platform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
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
	var guardedCalls, approvedCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/guarded" {
			guardedCalls.Add(1)
		}
		if r.URL.Path == "/approved" {
			approvedCalls.Add(1)
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
	notices := make(chan map[string]any, 20)
	feishu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var message map[string]any
		json.NewDecoder(r.Body).Decode(&message)
		notices <- message
		writeJSON(w, 200, map[string]any{"code": 0})
	}))
	defer feishu.Close()
	cfg := Config{FeishuWebhookURL: feishu.URL + "/hook", FeishuWebhookSecret: "sign-secret", PublicURL: "https://workbench.example/", DatabaseURL: u.String(), EncryptionKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), AdminUsername: "ADMIN", AdminPassword: "integration-admin-password", AllowedOrigins: []string{upstream.URL}, WebDist: t.TempDir()}
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
	if code := jsonRequest(t, alice, "GET", address+"/export?format=openapi", nil, nil); code != 200 {
		t.Fatalf("OpenAPI 导出失败：%d", code)
	}
	if code := jsonRequest(t, alice, "GET", address+"/export?format=higress", nil, nil); code != 400 {
		t.Fatalf("应拒绝已移除的格式：%d", code)
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
	if code := jsonRequest(t, alice, "GET", server.URL+"/api/tools", nil, &mappedTools); code != 200 || len(mappedTools) != 1 || len(mappedTools[0].OutputFields) != 2 || !reflect.DeepEqual(mappedTools[0].OutputFields[0], mapped.OutputFields[0]) {
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
	if code := jsonRequest(t, alice, "POST", server.URL+"/api/tools", whole, nil); code != 400 {
		t.Fatalf("完整请求体模式应被拒绝：%d", code)
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
		check(bob, "GET", sharedPath+"/export?format=openapi", nil, nil, 404)
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
		const message = "请填写 host_ip 或 admin"
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
	t.Run("开启审批的接口由审批人批准后由平台执行一次", func(t *testing.T) {
		check := func(c *http.Client, method, path string, body, out any, want int) {
			t.Helper()
			if got := jsonRequest(t, c, method, server.URL+path, body, out); got != want {
				t.Fatalf("%s %s 返回 %d，预期 %d", method, path, got, want)
			}
		}
		write := d
		write.Name, write.Method, write.URL, write.Description = "approvedWrite", "POST", upstream.URL+"/approved", "删除主机记录"
		write.Parameters = []Parameter{{Name: "host_ip", In: "body", Type: "string", Required: true}, d.Parameters[len(d.Parameters)-1]}
		write.Approval = &ApprovalRule{Approver: "self", ApproverName: "bob"}
		check(alice, "POST", "/api/tools", write, nil, 400)
		write.Approval = &ApprovalRule{Approver: "user", ApproverName: "nobody"}
		check(alice, "POST", "/api/tools", write, nil, 400)
		write.Approval = &ApprovalRule{Approver: "user", ApproverName: " BOB "}
		var record ToolRecord
		check(alice, "POST", "/api/tools", write, &record, 201)
		if record.Approval == nil || record.Approval.ApproverName != "bob" {
			t.Fatalf("审批人未规范化：%+v", record.Approval)
		}
		toolPath := fmt.Sprintf("/api/tools/%d", record.ID)
		check(alice, "POST", toolPath+"/publish", map[string]bool{"published": true}, nil, 200)
		list, err := session.ListTools(ctx, nil)
		if err != nil || !slices.ContainsFunc(list.Tools, func(tool *mcp.Tool) bool {
			return tool.Name == write.Name && strings.Contains(tool.Description, "调用需审批")
		}) {
			t.Fatalf("MCP 工具描述未提示审批：%v", err)
		}
		submit := func(s *mcp.ClientSession, name string, args map[string]any) int64 {
			t.Helper()
			result, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "尚未执行") {
				t.Fatalf("提交审批失败：%+v，%v", result, err)
			}
			var out struct{ ApprovalID int64 }
			data, _ := json.Marshal(result.StructuredContent)
			if json.Unmarshal(data, &out) != nil || out.ApprovalID == 0 {
				t.Fatalf("缺少审批单号：%s", data)
			}
			select {
			case notice := <-notices:
				text, _ := json.Marshal(notice)
				mac := hmac.New(sha256.New, []byte(fmt.Sprint(notice["timestamp"])+"\nsign-secret"))
				if notice["sign"] != base64.StdEncoding.EncodeToString(mac.Sum(nil)) || !strings.Contains(string(text), fmt.Sprintf("#%d", out.ApprovalID)) || !strings.Contains(string(text), "https://workbench.example/#approvals") ||
					strings.Contains(string(text), "192.0.2.") || strings.Contains(string(text), upstream.URL) || strings.Contains(string(text), "private-upstream-secret") {
					t.Fatalf("飞书通知内容不符合预期：%s", text)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("未收到飞书通知")
			}
			return out.ApprovalID
		}
		decide := func(c *http.Client, id int64, approve bool, want int) Approval {
			t.Helper()
			var item Approval
			check(c, "POST", fmt.Sprintf("/api/approvals/%d/decision", id), map[string]bool{"approve": approve}, &item, want)
			return item
		}
		if result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: write.Name, Arguments: map[string]any{}}); err != nil || !result.IsError {
			t.Fatal("参数无效时不应生成审批单")
		}
		first := submit(session, write.Name, map[string]any{"host_ip": "192.0.2.9"})
		if approvedCalls.Load() != 0 {
			t.Fatal("提交审批时已访问上游")
		}
		var items []Approval
		check(alice, "GET", "/api/approvals", nil, &items, 200)
		if len(items) != 1 || items[0].Status != "pending" || items[0].CanDecide || items[0].Approver != "bob" || items[0].Arguments != nil || items[0].Result != nil {
			t.Fatalf("申请人看到的审批单错误：%+v", items)
		}
		decide(alice, first, true, 409)
		decide(admin, first, true, 404)
		var detail Approval
		check(bob, "GET", fmt.Sprintf("/api/approvals/%d", first), nil, &detail, 200)
		if !detail.CanDecide || !strings.Contains(string(detail.Arguments), "192.0.2.9") || detail.Request != nil || detail.Method != "POST" || detail.Purpose != write.Description {
			t.Fatalf("审批人详情错误：%+v", detail)
		}
		check(alice, "GET", fmt.Sprintf("/api/approvals/%d", first), nil, &detail, 200)
		if detail.Request == nil || detail.Request.URL != upstream.URL+"/approved" || !strings.Contains(detail.Request.Body, "192.0.2.9") {
			t.Fatalf("接口维护者应看到最终请求：%+v", detail.Request)
		}
		done := decide(bob, first, true, 200)
		if done.Status != "executed" || done.Result == nil || done.Result.StatusCode != 200 || !strings.Contains(done.Result.Body, "192.0.2.9") || approvedCalls.Load() != 1 {
			t.Fatalf("批准后执行结果错误：%+v，上游 %d 次", done, approvedCalls.Load())
		}
		decide(bob, first, true, 409)
		decide(bob, first, false, 409)

		rejected := submit(session, write.Name, map[string]any{"host_ip": "192.0.2.10"})
		if item := decide(bob, rejected, false, 200); item.Status != "rejected" {
			t.Fatalf("拒绝状态错误：%+v", item)
		}
		expired := submit(session, write.Name, map[string]any{"host_ip": "192.0.2.11"})
		if _, err := a.db.Exec(ctx, "UPDATE approvals SET expires_at=now()-interval '1 second' WHERE id=$1", expired); err != nil {
			t.Fatal(err)
		}
		decide(bob, expired, true, 409)
		check(bob, "GET", fmt.Sprintf("/api/approvals/%d", expired), nil, &detail, 200)
		if detail.Status != "expired" || detail.CanDecide {
			t.Fatalf("过期状态错误：%+v", detail)
		}

		republished := submit(session, write.Name, map[string]any{"host_ip": "192.0.2.12"})
		changed := submit(session, write.Name, map[string]any{"host_ip": "192.0.2.13"})
		check(alice, "POST", toolPath+"/publish", map[string]bool{"published": false}, nil, 200)
		check(alice, "POST", toolPath+"/publish", map[string]bool{"published": true}, nil, 200)
		if item := decide(bob, republished, true, 200); item.Status != "executed" || approvedCalls.Load() != 2 {
			t.Fatalf("仅重新发布不应使审批单失效：%+v", item)
		}
		check(alice, "POST", toolPath+"/publish", map[string]bool{"published": false}, nil, 200)
		write.Approval = &ApprovalRule{Approver: "self"}
		check(alice, "PUT", toolPath, write, nil, 200)
		check(alice, "POST", toolPath+"/publish", map[string]bool{"published": true}, nil, 200)
		if item := decide(bob, changed, true, 200); item.Status != "failed" || !strings.Contains(item.Error, "已修改") || approvedCalls.Load() != 2 {
			t.Fatalf("配置修改后不应执行：%+v", item)
		}
		own := submit(session, write.Name, map[string]any{"host_ip": "192.0.2.14"})
		decide(bob, own, true, 404)
		if item := decide(alice, own, true, 200); item.Status != "executed" || approvedCalls.Load() != 3 {
			t.Fatalf("本人审批未执行：%+v", item)
		}
		check(alice, "POST", toolPath+"/publish", map[string]bool{"published": false}, nil, 200)
		unmapped := write
		unmapped.OutputFields = []OutputField{{Name: "missing", Source: "response", Value: "/missing"}}
		check(alice, "PUT", toolPath, unmapped, nil, 200)
		check(alice, "POST", toolPath+"/publish", map[string]bool{"published": true}, nil, 200)
		sent := submit(session, write.Name, map[string]any{"host_ip": "192.0.2.16"})
		if item := decide(alice, sent, true, 200); item.Status != "unknown" || !strings.Contains(item.Error, "结果未知") || strings.Contains(item.Error, "未执行") || approvedCalls.Load() != 4 {
			t.Fatalf("请求发出后的失败应标为结果未知：%+v，上游 %d 次", item, approvedCalls.Load())
		}

		shared := write
		shared.Name, shared.Approval = "sharedWrite", &ApprovalRule{Approver: "admin"}
		var sharedRecord ToolRecord
		check(admin, "POST", "/api/tools", shared, &sharedRecord, 201)
		sharedPath := fmt.Sprintf("/api/tools/%d", sharedRecord.ID)
		check(admin, "PATCH", sharedPath+"/visibility", map[string]bool{"public": true}, nil, 200)
		check(admin, "POST", sharedPath+"/publish", map[string]bool{"published": true}, nil, 200)
		check(bob, "POST", sharedPath+"/call", map[string]any{"arguments": map[string]any{"host_ip": "192.0.2.15"}}, nil, 403)
		var bobTools []ToolRecord
		check(bob, "GET", "/api/tools", nil, &bobTools, 200)
		if !slices.ContainsFunc(bobTools, func(item ToolRecord) bool {
			return item.ID == sharedRecord.ID && item.Approval != nil && item.URL == ""
		}) {
			t.Fatal("公共接口未向调用者标明需审批")
		}
		adminTicket := submit(bobSession, fmt.Sprintf("sharedWrite.public.%d", sharedRecord.ID), map[string]any{"host_ip": "192.0.2.15"})
		decide(bob, adminTicket, true, 409)
		if item := decide(admin, adminTicket, true, 200); item.Status != "executed" || item.Approver != "管理员" || item.DecidedBy != "admin" || approvedCalls.Load() != 5 {
			t.Fatalf("管理员审批未执行：%+v", item)
		}
		var stored []byte
		if err := a.db.QueryRow(ctx, "SELECT arguments FROM approvals WHERE id=$1", adminTicket).Scan(&stored); err != nil || bytes.Contains(stored, []byte("192.0.2.15")) {
			t.Fatalf("审批参数明文落库：%v", err)
		}
		check(alice, "DELETE", toolPath, nil, nil, 200)
		check(alice, "GET", fmt.Sprintf("/api/approvals/%d", own), nil, &detail, 200)
		if detail.ToolName != write.Name || detail.ToolID != nil {
			t.Fatalf("删除接口后审批记录丢失：%+v", detail)
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

func TestGrafanaIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("设置 TEST_DATABASE_URL 后运行 Grafana 集成测试")
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
	namespace := fmt.Sprintf("grafana_int_%d", time.Now().UnixNano())
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

	// 模拟 Grafana 服务：校验 Bearer / Basic / Cookie 三种认证方式，响应 /api/datasources 与 /api/ds/query
	grafana := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authOK := r.Header.Get("Authorization") == "Bearer glsa_test_token" ||
			r.Header.Get("Cookie") == "grafana_session=cookie_ok"
		if user, pass, ok := r.BasicAuth(); ok && user == "ops" && pass == "ops-secret-123" {
			authOK = true
		}
		if !authOK {
			http.Error(w, `{"message":"Unauthorized"}`, 401)
			return
		}
		switch r.URL.Path {
		case "/api/datasources":
			writeJSON(w, 200, []map[string]any{
				{"uid": "prom-1", "name": "Prometheus", "type": "prometheus", "isDefault": true, "password": "secret-should-not-leak"},
				{"uid": "vmlogs-1", "name": "VictoriaLogs", "type": "victoriametrics-logs-datasource"},
			})
		case "/api/ds/query":
			var reqBody struct {
				From    string           `json:"from"`
				To      string           `json:"to"`
				Queries []map[string]any `json:"queries"`
			}
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				http.Error(w, "bad body", 400)
				return
			}
			results := map[string]any{}
			for _, q := range reqBody.Queries {
				refID := q["refId"].(string)
				expr, _ := q["expr"].(string)
				fields := []map[string]any{
					{"name": "Time", "type": "time"},
					{"name": "Expr", "type": "string"},
					{"name": "Range", "type": "string"},
				}
				values := [][]any{
					{int64(1700000000000)},
					{expr},
					{reqBody.From + ".." + reqBody.To},
				}
				if refID == "B" {
					fields = append(fields,
						map[string]any{"name": "_msg", "type": "string"},
						map[string]any{"name": "pod_uid", "type": "string"},
					)
					values = append(values,
						[]any{`10.1.2.3 [ERROR] {"trace_id":"tr-int-1","msg":"timeout","debug":"drop"}`},
						[]any{"noisy-pod-uid"},
					)
				}
				results[refID] = map[string]any{
					"status": 200,
					"frames": []map[string]any{{
						"schema": map[string]any{
							"name":   refID,
							"refId":  refID,
							"fields": fields,
						},
						"data": map[string]any{
							"values": values,
						},
					}},
				}
			}
			writeJSON(w, 200, map[string]any{"results": results})
		default:
			http.NotFound(w, r)
		}
	}))
	defer grafana.Close()

	cfg := Config{
		DatabaseURL:    u.String(),
		EncryptionKey:  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
		AdminUsername:  "admin",
		AdminPassword:  "grafana-admin-password",
		AllowedOrigins: []string{grafana.URL},
		WebDist:        t.TempDir(),
	}
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	server := httptest.NewServer(a.Handler())
	defer server.Close()

	newClient := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar, Timeout: 10 * time.Second}
	}
	admin, member := newClient(), newClient()
	if code := jsonRequest(t, admin, "POST", server.URL+"/api/auth/login", map[string]string{"username": "admin", "password": "grafana-admin-password"}, nil); code != 200 {
		t.Fatalf("管理员登录失败：%d", code)
	}
	if code := jsonRequest(t, member, "POST", server.URL+"/api/auth/register", map[string]string{"username": "alice", "password": "alice-password-123"}, nil); code != 200 {
		t.Fatalf("成员注册失败：%d", code)
	}

	// 普通成员不能创建连接
	if code := jsonRequest(t, member, "POST", server.URL+"/api/grafana/connections", map[string]any{
		"name": "member-try", "baseUrl": grafana.URL, "authType": "bearer", "token": "glsa_test_token",
	}, nil); code != 403 {
		t.Fatalf("普通成员创建连接应返回 403，实际 %d", code)
	}

	// 管理员分别验证 Bearer、Basic、Cookie 三种连接
	var bearerConn, basicConn, cookieConn GrafanaConnection
	if code := jsonRequest(t, admin, "POST", server.URL+"/api/grafana/connections", map[string]any{
		"name": "Prod Grafana", "baseUrl": grafana.URL, "authType": "bearer", "token": "glsa_test_token",
	}, &bearerConn); code != 201 {
		t.Fatalf("创建 Bearer 连接失败：%d", code)
	}
	if code := jsonRequest(t, admin, "POST", server.URL+"/api/grafana/connections", map[string]any{
		"name": "Basic Grafana", "baseUrl": grafana.URL, "authType": "basic", "username": "ops", "password": "ops-secret-123",
	}, &basicConn); code != 201 || basicConn.Username != "ops" {
		t.Fatalf("创建 Basic 连接失败：%d %+v", code, basicConn)
	}
	if code := jsonRequest(t, admin, "POST", server.URL+"/api/grafana/connections", map[string]any{
		"name": "Cookie Grafana", "baseUrl": grafana.URL, "authType": "cookie", "cookie": "grafana_session=cookie_ok",
	}, &cookieConn); code != 201 {
		t.Fatalf("创建 Cookie 连接失败：%d", code)
	}
	for _, cid := range []int64{bearerConn.ID, basicConn.ID, cookieConn.ID} {
		var testRes struct {
			OK              bool `json:"ok"`
			DatasourceCount int  `json:"datasourceCount"`
		}
		if code := jsonRequest(t, admin, "POST", fmt.Sprintf("%s/api/grafana/connections/%d/test", server.URL, cid), map[string]any{}, &testRes); code != 200 || !testRes.OK || testRes.DatasourceCount != 2 {
			t.Fatalf("测试连接 %d 失败：%d %+v", cid, code, testRes)
		}
	}
	// 管理员编辑 Basic 连接留空密码应保持原密码可用
	if code := jsonRequest(t, admin, "PUT", fmt.Sprintf("%s/api/grafana/connections/%d", server.URL, basicConn.ID), map[string]any{
		"name": "Basic Grafana Updated", "baseUrl": grafana.URL, "authType": "basic", "username": "ops", "password": "",
	}, &basicConn); code != 200 {
		t.Fatalf("更新 Basic 连接失败：%d", code)
	}
	if code := jsonRequest(t, admin, "POST", fmt.Sprintf("%s/api/grafana/connections/%d/test", server.URL, basicConn.ID), map[string]any{}, nil); code != 200 {
		t.Fatalf("留空密码更新后测试连接失败：%d", code)
	}

	// 普通成员读取连接列表不含 Basic 用户名或凭据，且可拉取数据源列表（过滤敏感字段）
	var memberConns []GrafanaConnection
	if code := jsonRequest(t, member, "GET", server.URL+"/api/grafana/connections", nil, &memberConns); code != 200 || len(memberConns) != 3 {
		t.Fatalf("成员获取连接列表失败：%d %+v", code, memberConns)
	}
	for _, c := range memberConns {
		if c.Username != "" {
			t.Fatalf("普通成员不应看到 Basic 用户名：%+v", c)
		}
	}
	var dsList []GrafanaDatasource
	if code := jsonRequest(t, member, "GET", fmt.Sprintf("%s/api/grafana/connections/%d/datasources", server.URL, bearerConn.ID), nil, &dsList); code != 200 || len(dsList) != 2 {
		t.Fatalf("成员拉取数据源失败：%d %+v", code, dsList)
	}

	// 成员创建多条查询 Grafana 工具并调试、发布
	def := Definition{
		Kind:           "grafana",
		Name:           "query_host_multi",
		Description:    "多查询按主机 IP 查指标与日志",
		TimeoutSeconds: 15,
		Grafana: &GrafanaConfig{
			ConnectionID: bearerConn.ID,
			FromRequired: false,
			DefaultFrom:  "now-1h",
			ToRequired:   false,
			DefaultTo:    "now",
			Queries: []GrafanaQuery{
				{RefID: "A", DatasourceUID: "prom-1", DatasourceType: "prometheus", DatasourceName: "Prometheus", DataType: "metrics", Expr: `up{instance=~"{{host_ip}}.*"}`},
				{
					RefID:          "B",
					DatasourceUID:  "vmlogs-1",
					DatasourceType: "victoriametrics-logs-datasource",
					DatasourceName: "VictoriaLogs",
					DataType:       "logs",
					Expr:           `host:"{{host_ip}}"`,
					MaxLines:       50,
					LogProcessors: []LogProcessor{
						{Type: "regex", SourceKey: "_msg", Regex: `^(\S+)\s+\[(\w+)\]\s+(\{.*\})$`, Keys: []string{"ip", "level", "payload"}},
						{Type: "json", SourceKey: "payload", ExpandDepth: 1},
						{Type: "keep_fields", Fields: []string{"Time", "Expr", "Range", "ip", "level", "trace_id", "msg"}},
					},
				},
			},
		},
		Parameters: []Parameter{
			{Name: "host_ip", Description: "主机 IP", In: "query", Type: "string", Required: true, NonEmpty: true},
		},
	}
	var tool ToolRecord
	if code := jsonRequest(t, member, "POST", server.URL+"/api/tools", def, &tool); code != 201 {
		t.Fatalf("创建 Grafana 工具失败：%d", code)
	}

	// 被工具引用的连接不能删除
	if code := jsonRequest(t, admin, "DELETE", fmt.Sprintf("%s/api/grafana/connections/%d", server.URL, bearerConn.ID), nil, nil); code != 409 {
		t.Fatalf("删除被引用连接应返回 409，实际 %d", code)
	}
	// Grafana 工具不支持 OpenAPI 导出
	if code := jsonRequest(t, member, "GET", fmt.Sprintf("%s/api/tools/%d/export?format=openapi", server.URL, tool.ID), nil, nil); code != 400 {
		t.Fatalf("Grafana 工具导出 OpenAPI 应返回 400，实际 %d", code)
	}

	// 调试调用：不传 from/to 使用默认值，并检查 rawBody 存在
	var callRes CallResult
	if code := jsonRequest(t, member, "POST", fmt.Sprintf("%s/api/tools/%d/call", server.URL, tool.ID), map[string]any{
		"arguments": map[string]any{"host_ip": "10.1.2.3"},
		"raw":       true,
	}, &callRes); code != 200 || callRes.StatusCode != 200 || callRes.RawBody == "" {
		t.Fatalf("Grafana 调试调用失败：%d %+v", code, callRes)
	}
	if !strings.Contains(callRes.Body, `up{instance=~\"10.1.2.3.*\"}`) && !strings.Contains(callRes.Body, `10.1.2.3`) {
		t.Fatalf("整理后的结果未包含替换后的表达式：%s", callRes.Body)
	}
	if !strings.Contains(callRes.Body, "now-1h..now") {
		t.Fatalf("整理后的结果未使用默认时间：%s", callRes.Body)
	}
	if !strings.Contains(callRes.Body, `"dataType":"metrics"`) || !strings.Contains(callRes.Body, `"dataType":"logs"`) {
		t.Fatalf("整理后的结果缺少 metrics/logs 数据类型标签：%s", callRes.Body)
	}
	if !strings.Contains(callRes.Body, `"trace_id":"tr-int-1"`) || strings.Contains(callRes.Body, "noisy-pod-uid") || strings.Contains(callRes.Body, `"debug":"drop"`) {
		t.Fatalf("Logs 处理器未对调试结果生效：%s", callRes.Body)
	}
	// 测试日志样例分步预览接口
	var previewRes struct {
		Output map[string]any `json:"output"`
		Steps  []struct {
			Matched bool `json:"matched"`
		} `json:"steps"`
	}
	if code := jsonRequest(t, member, "POST", server.URL+"/api/grafana/log-processors/preview", map[string]any{
		"sample":     `10.1.2.3 [ERROR] {"trace_id":"tr-prev-1","msg":"timeout","debug":"drop"}`,
		"processors": def.Grafana.Queries[1].LogProcessors,
	}, &previewRes); code != 200 || len(previewRes.Steps) != 3 || !previewRes.Steps[0].Matched || previewRes.Output["trace_id"] != "tr-prev-1" {
		t.Fatalf("日志处理预览接口异常：%d %+v", code, previewRes)
	}

	// 配置工具级输出字段处理器（从 /results/B/frames/0/rows 提取并做正则+JSON+保留字段），验证预览接口与 MCP 输出
	def.OutputFields = []OutputField{
		{
			Name:   "logs",
			Source: "response",
			Value:  "/results/B/frames/0/rows/*/_msg",
			Processors: []LogProcessor{
				{Type: "regex", Regex: `^(\S+)\s+\[(\w+)\]\s+(\{.*\})$`, Keys: []string{"ip", "level", "payload"}},
				{Type: "json", SourceKey: "payload", ExpandDepth: 1},
				{Type: "keep_fields", Fields: []string{"ip", "level", "trace_id", "msg"}},
			},
		},
		{Name: "range", Source: "response", Value: "/results/A/frames/0/rows/0/Range"},
		{Name: "metricType", Source: "response", Value: "/results/A/dataType"},
		{Name: "logType", Source: "response", Value: "/results/B/dataType"},
		{Name: "expr", Source: "response", Value: "/results/A/frames/0/rows/0/Expr"},
	}
	def.Grafana.Queries[1].LogProcessors = nil
	if code := jsonRequest(t, member, "PUT", fmt.Sprintf("%s/api/tools/%d", server.URL, tool.ID), def, &tool); code != 200 {
		t.Fatalf("更新输出字段处理器失败：%d", code)
	}

	// 发布并通过真实 MCP 客户端调用（覆盖 from/to），验证输出字段处理器精简后的 MCP 返回值
	if code := jsonRequest(t, member, "POST", fmt.Sprintf("%s/api/tools/%d/publish", server.URL, tool.ID), map[string]bool{"published": true}, nil); code != 200 {
		t.Fatalf("发布 Grafana 工具失败：%d", code)
	}
	var tokenResp struct {
		Token string `json:"token"`
	}
	if code := jsonRequest(t, member, "POST", server.URL+"/api/tokens", map[string]string{"name": "mcp-grafana"}, &tokenResp); code != 201 {
		t.Fatalf("创建令牌失败：%d", code)
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   server.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: tokenResp.Token}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "query_host_multi" {
		t.Fatalf("MCP tools/list 不符：%v %+v", err, list)
	}
	mcpRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "query_host_multi",
		Arguments: map[string]any{
			"host_ip": "10.9.9.9",
			"from":    "now-15m",
			"to":      "now",
		},
	})
	if err != nil || mcpRes.IsError {
		t.Fatalf("MCP 调用 Grafana 工具失败：%v %+v", err, mcpRes)
	}
	text := mcpRes.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "10.9.9.9") || !strings.Contains(text, "now-15m..now") || !strings.Contains(text, `"metricType":"metrics"`) || !strings.Contains(text, `"logType":"logs"`) || !strings.Contains(text, `"trace_id":"tr-int-1"`) || strings.Contains(text, "noisy-pod-uid") || strings.Contains(text, `"debug":"drop"`) {
		t.Fatalf("MCP 返回内容不符：%s", text)
	}
}
