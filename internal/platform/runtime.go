package platform

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CallResult struct {
	StatusCode  int    `json:"statusCode"`
	Body        string `json:"body"`
	ContentType string `json:"contentType"`
	DurationMS  int64  `json:"durationMs"`
}

func (a *App) execute(ctx context.Context, d Definition, args map[string]any) (CallResult, error) {
	var result CallResult
	request, err := d.BuildRequest(args)
	if err != nil {
		return result, err
	}
	if !a.allowed[request.URL.Scheme+"://"+strings.ToLower(request.URL.Host)] {
		return result, fmt.Errorf("该上游地址未获准访问，请联系管理员配置 UPSTREAM_ORIGINS")
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
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil {
		return result, fmt.Errorf("读取上游响应失败")
	}
	if len(data) > 2<<20 {
		return result, fmt.Errorf("上游响应超过 2 MiB 限制")
	}
	return CallResult{StatusCode: response.StatusCode, Body: string(data), ContentType: response.Header.Get("Content-Type"), DurationMS: time.Since(start).Milliseconds()}, nil
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
		if !record.Published {
			continue
		}
		id := record.ID
		server.AddTool(&mcp.Tool{Name: record.Name, Description: record.Description, InputSchema: record.InputSchema()}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tool, err := a.getTool(ctx, id, User{ID: user.ID, Role: "member"})
			if err != nil || !tool.Published {
				return toolError("工具不存在或已停用"), nil
			}
			args := map[string]any{}
			if len(request.Params.Arguments) > 0 {
				if args, err = tool.Definition.decodeArguments(request.Params.Arguments); err != nil || args == nil {
					return toolError("工具参数必须是 JSON 对象"), nil
				}
			}
			result, err := a.execute(ctx, tool.Definition, args)
			if err != nil {
				return toolError(err.Error()), nil
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
