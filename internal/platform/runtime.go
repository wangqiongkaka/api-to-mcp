package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxResponseBytes = 2 << 20

type CallResult struct {
	StatusCode   int    `json:"statusCode"`
	Body         string `json:"body"`
	RawBody      string `json:"rawBody,omitempty"`
	ContentType  string `json:"contentType"`
	DurationMS   int64  `json:"durationMs"`
	OutputMapped bool   `json:"outputMapped,omitempty"`
}

// prepare 构造上游请求并确认目标源地址已获准访问。
func (a *App) prepare(ctx context.Context, d Definition, args map[string]any) (*http.Request, string, string, error) {
	var (
		request      *http.Request
		resolvedFrom string
		resolvedTo   string
		err          error
	)
	if d.isGrafana() {
		if d.Grafana == nil || d.Grafana.ConnectionID < 1 {
			return nil, "", "", fmt.Errorf("请选择 Grafana 连接")
		}
		conn, err := a.getGrafanaConnection(ctx, d.Grafana.ConnectionID)
		if err != nil {
			if isNotFound(err) {
				return nil, "", "", fmt.Errorf("关联的 Grafana 连接不存在或已被删除")
			}
			return nil, "", "", fmt.Errorf("读取 Grafana 连接失败")
		}
		request, resolvedFrom, resolvedTo, err = d.buildGrafanaRequest(conn, args)
		if err != nil {
			return nil, "", "", err
		}
	} else {
		request, err = d.BuildRequest(args)
		if err != nil {
			return nil, "", "", err
		}
	}
	if !a.allowed[request.URL.Scheme+"://"+strings.ToLower(request.URL.Host)] {
		return nil, "", "", fmt.Errorf("该上游地址未获准访问，请联系管理员配置 UPSTREAM_ORIGINS")
	}
	return request, resolvedFrom, resolvedTo, nil
}

func (a *App) execute(ctx context.Context, d Definition, args map[string]any) (CallResult, error) {
	return a.executeWithRaw(ctx, d, args, false)
}

func (a *App) executeWithRaw(ctx context.Context, d Definition, args map[string]any, includeRaw bool) (CallResult, error) {
	var result CallResult
	request, resolvedFrom, resolvedTo, err := a.prepare(ctx, d, args)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(d.TimeoutSeconds)*time.Second)
	defer cancel()
	start := time.Now()
	response, err := a.client.Do(request.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("上游请求超时或已取消")
		}
		return result, fmt.Errorf("上游请求失败，请检查服务地址及网络连通性")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return result, fmt.Errorf("读取上游响应失败")
	}
	if len(data) > maxResponseBytes {
		return result, fmt.Errorf("上游响应超过 2 MiB 限制")
	}
	result = CallResult{StatusCode: response.StatusCode, ContentType: response.Header.Get("Content-Type"), DurationMS: time.Since(start).Milliseconds()}
	if d.isGrafana() && response.StatusCode >= 200 && response.StatusCode < 300 {
		if includeRaw {
			result.RawBody = string(data)
		}
		formatted, err := formatGrafanaResponse(resolvedFrom, resolvedTo, data)
		if err != nil {
			return result, fmt.Errorf("解析 Grafana 响应失败")
		}
		data = formatted
		result.ContentType = "application/json"
	}
	if len(d.OutputFields) > 0 && response.StatusCode >= 200 && response.StatusCode < 300 {
		data, err = mapOutput(d.OutputFields, data)
		if err != nil {
			return result, err
		}
		result.ContentType = "application/json"
		result.OutputMapped = true
	}
	result.Body = string(data)
	return result, nil
}

func (a *App) serveMCP(w http.ResponseWriter, r *http.Request) {
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || len(authorization) > 256 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		fail(w, 401, "请提供 MCP 访问令牌")
		return
	}
	var user User
	err := a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.role,u.disabled FROM access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND NOT u.disabled`, hashToken(strings.TrimPrefix(authorization, "Bearer "))).Scan(&user.ID, &user.Username, &user.Role, &user.Disabled)
	if err != nil {
		if isNotFound(err) {
			fail(w, 401, "MCP 令牌无效或已撤销")
		} else {
			fail(w, 503, "MCP 身份验证暂时不可用")
		}
		return
	}
	tools, err := a.listTools(r.Context(), user, true)
	if err != nil {
		fail(w, 503, "无法加载工具")
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "api-to-mcp", Version: "0.1.0"}, nil)
	for _, record := range tools {
		id, name, description := record.ID, record.mcpName(user.ID), record.Description
		if record.Approval != nil {
			description += "\n\n调用需审批：提交后不会立即执行，审批通过后由平台执行并保存结果。"
		}
		server.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: record.InputSchema()}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tool, err := a.getTool(ctx, id, User{ID: user.ID}, true)
			if err != nil || !tool.Published || tool.mcpName(user.ID) != name {
				return toolError("工具不存在或已停用"), nil
			}
			args := map[string]any{}
			if len(request.Params.Arguments) > 0 {
				if args, err = tool.Definition.decodeArguments(request.Params.Arguments); err != nil || args == nil {
					return toolError("工具参数必须是 JSON 对象"), nil
				}
			}
			if tool.Approval != nil {
				return a.submitApproval(ctx, user, tool, args), nil
			}
			result, err := a.execute(ctx, tool.Definition, args)
			if err != nil {
				return toolError(err.Error()), nil
			}
			if result.OutputMapped {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result.Body}}, StructuredContent: json.RawMessage(result.Body)}, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("HTTP %d\n%s", result.StatusCode, result.Body)}}, StructuredContent: result, IsError: result.StatusCode < 200 || result.StatusCode >= 300}, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	handler.ServeHTTP(w, r)
}

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}, IsError: true}
}
