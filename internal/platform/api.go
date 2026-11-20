package platform

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.Ping(r.Context()); err != nil {
			fail(w, 503, "数据库不可用")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/auth/register", a.auth)
	mux.HandleFunc("POST /api/auth/login", a.auth)
	mux.HandleFunc("POST /api/auth/logout", a.requireUser(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("api_mcp_session")
		if _, err := a.db.Exec(r.Context(), "DELETE FROM sessions WHERE token_hash=$1", hashToken(cookie.Value)); err != nil {
			fail(w, 500, "退出失败，请重试")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "api_mcp_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteStrictMode})
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/me", a.requireUser(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, currentUser(r)) }))
	mux.HandleFunc("GET /api/settings", a.requireUser(func(w http.ResponseWriter, r *http.Request) {
		origins := []string{}
		for origin := range a.allowed {
			origins = append(origins, origin)
		}
		sort.Strings(origins)
		writeJSON(w, 200, map[string]any{"allowedOrigins": origins})
	}))
	mux.HandleFunc("GET /api/tools", a.requireUser(a.tools))
	mux.HandleFunc("POST /api/tools", a.requireUser(a.tools))
	mux.HandleFunc("PUT /api/tools/{id}", a.requireUser(a.tool))
	mux.HandleFunc("DELETE /api/tools/{id}", a.requireUser(a.tool))
	mux.HandleFunc("POST /api/tools/{id}/publish", a.requireUser(a.publish))
	mux.HandleFunc("POST /api/tools/{id}/call", a.requireUser(a.call))
	mux.HandleFunc("POST /api/output-preview", a.requireUser(outputPreview))
	mux.HandleFunc("GET /api/tools/{id}/export", a.requireUser(a.export))
	mux.HandleFunc("GET /api/tokens", a.requireUser(a.tokens))
	mux.HandleFunc("POST /api/tokens", a.requireUser(a.tokens))
	mux.HandleFunc("DELETE /api/tokens/{id}", a.requireUser(a.tokens))
	mux.HandleFunc("GET /api/users", a.requireUser(a.users))
	mux.HandleFunc("PATCH /api/users/{id}", a.requireUser(a.users))
	mux.HandleFunc("/mcp", a.serveMCP)
	mux.HandleFunc("/", a.static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && !checkOrigin(r) {
			fail(w, 403, "请求来源不匹配")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		mux.ServeHTTP(w, r)
	})
}

func (a *App) tools(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if r.Method == "GET" {
		tools, err := a.listTools(r.Context(), u, false)
		if err != nil {
			fail(w, 500, "加载接口失败")
			return
		}
		writeJSON(w, 200, tools)
		return
	}
	var d Definition
	if !decode(w, r, &d) {
		return
	}
	if err := d.Validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	data, err := a.encrypt(d)
	if err != nil {
		fail(w, 500, "配置加密失败")
		return
	}
	var id int64
	err = a.db.QueryRow(r.Context(), "INSERT INTO tools(owner_id,name,definition) VALUES($1,$2,$3) RETURNING id", u.ID, d.Name, data).Scan(&id)
	if err != nil {
		if isDuplicate(err) {
			fail(w, 409, "你已有同名工具，请使用其他名称")
		} else {
			fail(w, 500, "保存接口失败")
		}
		return
	}
	record, err := a.getTool(r.Context(), id, u)
	if err != nil {
		fail(w, 500, "读取新接口失败")
		return
	}
	writeJSON(w, 201, record)
}

func (a *App) record(w http.ResponseWriter, r *http.Request) (ToolRecord, bool) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err.Error())
		return ToolRecord{}, false
	}
	record, err := a.getTool(r.Context(), id, currentUser(r))
	if err != nil {
		if isNotFound(err) {
			fail(w, 404, "接口不存在")
		} else {
			fail(w, 500, "读取接口失败")
		}
		return ToolRecord{}, false
	}
	return record, true
}

func (a *App) tool(w http.ResponseWriter, r *http.Request) {
	record, ok := a.record(w, r)
	if !ok {
		return
	}
	if r.Method == "DELETE" {
		if _, err := a.db.Exec(r.Context(), "DELETE FROM tools WHERE id=$1", record.ID); err != nil {
			fail(w, 500, "删除失败")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if record.Published {
		fail(w, 409, "请先停用已发布接口，再保存修改")
		return
	}
	var d Definition
	if !decode(w, r, &d) {
		return
	}
	if err := d.Validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	data, err := a.encrypt(d)
	if err != nil {
		fail(w, 500, "配置加密失败")
		return
	}
	tag, err := a.db.Exec(r.Context(), "UPDATE tools SET name=$1,definition=$2,updated_at=now() WHERE id=$3 AND NOT published", d.Name, data, record.ID)
	if err != nil {
		if isDuplicate(err) {
			fail(w, 409, "工具名称已存在")
		} else {
			fail(w, 500, "更新接口失败")
		}
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 409, "接口已被发布或删除，请刷新后重试")
		return
	}
	record, err = a.getTool(r.Context(), record.ID, currentUser(r))
	if err != nil {
		fail(w, 500, "读取接口失败")
		return
	}
	writeJSON(w, 200, record)
}

func (a *App) publish(w http.ResponseWriter, r *http.Request) {
	record, ok := a.record(w, r)
	if !ok {
		return
	}
	var input struct {
		Published bool `json:"published"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Published {
		if err := record.Validate(); err != nil {
			fail(w, 400, err.Error())
			return
		}
		u, _ := url.Parse(record.URL)
		if !a.allowed[u.Scheme+"://"+strings.ToLower(u.Host)] {
			fail(w, 400, "上游地址尚未获准访问，请联系管理员配置 UPSTREAM_ORIGINS")
			return
		}
	}
	if _, err := a.db.Exec(r.Context(), "UPDATE tools SET published=$1,updated_at=now() WHERE id=$2", input.Published, record.ID); err != nil {
		fail(w, 500, "发布状态更新失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"published": input.Published})
}

func (a *App) call(w http.ResponseWriter, r *http.Request) {
	record, ok := a.record(w, r)
	if !ok {
		return
	}
	var input struct {
		Arguments json.RawMessage `json:"arguments"`
		Raw       bool            `json:"raw"`
	}
	if !decode(w, r, &input) {
		return
	}
	args, err := record.Definition.decodeArguments(input.Arguments)
	if err != nil || args == nil {
		fail(w, 400, "arguments 必须是 JSON 对象，无参数时传入空对象")
		return
	}
	if input.Raw {
		record.OutputFields = nil
	}
	result, err := a.execute(r.Context(), record.Definition, args)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, result)
}

func outputPreview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body   string        `json:"body"`
		Fields []OutputField `json:"fields"`
	}
	// JSON 字符串中一个控制字符最多占用六个字节。
	if !decodeLimited(w, r, &input, 6*maxResponseBytes+(256<<10)) {
		return
	}
	if len(input.Body) > maxResponseBytes {
		fail(w, 400, "原始响应超过 2 MiB 限制")
		return
	}
	if err := validateOutputFields(input.Fields); err != nil {
		fail(w, 400, err.Error())
		return
	}
	result, err := mapOutput(input.Fields, []byte(input.Body))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"body": string(result)})
}

func (a *App) export(w http.ResponseWriter, r *http.Request) {
	record, ok := a.record(w, r)
	if !ok {
		return
	}
	var data []byte
	var err error
	format := r.URL.Query().Get("format")
	switch format {
	case "openapi":
		data, err = record.OpenAPI()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	case "higress":
		data, err = record.Higress()
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	default:
		fail(w, 400, "导出格式应为 openapi 或 higress")
		return
	}
	if err != nil {
		fail(w, 400, "生成配置失败："+err.Error())
		return
	}
	w.Write(data)
}

func (a *App) tokens(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if r.Method == "GET" {
		rows, err := a.db.Query(r.Context(), "SELECT id,name,prefix,created_at FROM access_tokens WHERE user_id=$1 ORDER BY id DESC", u.ID)
		if err != nil {
			fail(w, 500, "读取令牌失败")
			return
		}
		defer rows.Close()
		type token struct {
			ID        int64     `json:"id"`
			Name      string    `json:"name"`
			Prefix    string    `json:"prefix"`
			CreatedAt time.Time `json:"createdAt"`
		}
		items := []token{}
		for rows.Next() {
			var item token
			if err = rows.Scan(&item.ID, &item.Name, &item.Prefix, &item.CreatedAt); err != nil {
				fail(w, 500, "读取令牌失败")
				return
			}
			items = append(items, item)
		}
		if rows.Err() != nil {
			fail(w, 500, "读取令牌失败")
			return
		}
		writeJSON(w, 200, items)
		return
	}
	if r.Method == "DELETE" {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		result, err := a.db.Exec(r.Context(), "DELETE FROM access_tokens WHERE id=$1 AND user_id=$2", id, u.ID)
		if err != nil {
			fail(w, 500, "撤销令牌失败")
			return
		}
		if result.RowsAffected() == 0 {
			fail(w, 404, "令牌不存在")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 80 {
		fail(w, 400, "请输入不超过 80 字节的令牌名称")
		return
	}
	token := "mcp_" + randomToken()
	var id int64
	if err := a.db.QueryRow(r.Context(), "INSERT INTO access_tokens(user_id,name,token_hash,prefix) VALUES($1,$2,$3,$4) RETURNING id", u.ID, input.Name, hashToken(token), token[:12]).Scan(&id); err != nil {
		fail(w, 500, "创建令牌失败")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "token": token})
}

func (a *App) users(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Role != "admin" {
		fail(w, 403, "仅管理员可管理用户")
		return
	}
	if r.Method == "PATCH" {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		var input struct {
			Disabled bool `json:"disabled"`
		}
		if !decode(w, r, &input) {
			return
		}
		tx, err := a.db.Begin(r.Context())
		if err != nil {
			fail(w, 500, "更新用户失败")
			return
		}
		defer tx.Rollback(r.Context())
		tag, err := tx.Exec(r.Context(), "UPDATE users SET disabled=$1 WHERE id=$2 AND role='member'", input.Disabled, id)
		if err != nil {
			fail(w, 500, "更新用户失败")
			return
		}
		if tag.RowsAffected() == 0 {
			fail(w, 400, "用户不存在或属于受保护的管理员账户")
			return
		}
		if input.Disabled {
			if _, err = tx.Exec(r.Context(), "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
				fail(w, 500, "撤销会话失败")
				return
			}
			if _, err = tx.Exec(r.Context(), "DELETE FROM access_tokens WHERE user_id=$1", id); err != nil {
				fail(w, 500, "撤销令牌失败")
				return
			}
		}
		if err = tx.Commit(r.Context()); err != nil {
			fail(w, 500, "更新用户失败")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	rows, err := a.db.Query(r.Context(), "SELECT id,username,role,disabled FROM users ORDER BY id")
	if err != nil {
		fail(w, 500, "读取用户失败")
		return
	}
	defer rows.Close()
	items := []User{}
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled); err != nil {
			fail(w, 500, "读取用户失败")
			return
		}
		items = append(items, u)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取用户失败")
		return
	}
	writeJSON(w, 200, items)
}

func (a *App) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, 404, "接口不存在")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	path := filepath.Join(a.webDist, filepath.FromSlash(strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")))
	if stat, err := os.Stat(path); err != nil || stat.IsDir() {
		path = filepath.Join(a.webDist, "index.html")
	}
	if _, err := os.Stat(path); err != nil {
		fail(w, 503, "前端尚未构建，请先在 web 目录运行 npm run build")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}
