package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const grafanaSchema = `
CREATE TABLE IF NOT EXISTS grafana_connections (
 id BIGSERIAL PRIMARY KEY,
 name TEXT UNIQUE NOT NULL,
 base_url TEXT NOT NULL,
 auth_type TEXT NOT NULL CHECK(auth_type IN ('bearer','basic','cookie')),
 credentials BYTEA NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

type GrafanaConfig struct {
	ConnectionID int64          `json:"connectionId"`
	FromRequired bool           `json:"fromRequired"`
	DefaultFrom  string         `json:"defaultFrom,omitempty"`
	ToRequired   bool           `json:"toRequired"`
	DefaultTo    string         `json:"defaultTo,omitempty"`
	Queries      []GrafanaQuery `json:"queries"`
}

type GrafanaQuery struct {
	RefID          string `json:"refId"`
	DatasourceUID  string `json:"datasourceUid"`
	DatasourceType string `json:"datasourceType"`
	DatasourceName string `json:"datasourceName,omitempty"`
	Expr           string `json:"expr"`
	QueryMode      string `json:"queryMode,omitempty"`
	MaxDataPoints  int    `json:"maxDataPoints,omitempty"`
	Interval       string `json:"interval,omitempty"`
	MaxLines       int    `json:"maxLines,omitempty"`
	TimeField      string `json:"timeField,omitempty"`
}

type GrafanaConnection struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	BaseURL   string    `json:"baseUrl"`
	AuthType  string    `json:"authType"`
	Username  string    `json:"username,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type GrafanaDatasource struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

type grafanaCredentials struct {
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Cookie   string `json:"cookie,omitempty"`
}

type grafanaConnectionRecord struct {
	GrafanaConnection
	Credentials grafanaCredentials
}

type grafanaConnectionInput struct {
	Name     string `json:"name"`
	BaseURL  string `json:"baseUrl"`
	AuthType string `json:"authType"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Cookie   string `json:"cookie,omitempty"`
}

var (
	grafanaRefIDPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)
	grafanaVarPattern    = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_.-]{0,127})\}\}`)
	grafanaEmptyVarCheck = regexp.MustCompile(`\{\{\s*\}\}`)
)

func normalizeGrafanaBaseURL(raw string) (string, string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(trimmed)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(trimmed) > 2048 {
		return "", "", fmt.Errorf("请输入不含账号信息、查询串或片段的完整 Grafana HTTP/HTTPS 地址")
	}
	origin := u.Scheme + "://" + strings.ToLower(u.Host)
	return trimmed, origin, nil
}

func validateGrafanaConnection(input grafanaConnectionInput, existing *grafanaCredentials) (string, string, string, grafanaCredentials, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 80 {
		return "", "", "", grafanaCredentials{}, fmt.Errorf("连接名称不能为空且不超过 80 字节")
	}
	baseURL, origin, err := normalizeGrafanaBaseURL(input.BaseURL)
	if err != nil {
		return "", "", "", grafanaCredentials{}, err
	}
	var creds grafanaCredentials
	switch input.AuthType {
	case "bearer":
		token := strings.TrimSpace(input.Token)
		if token == "" && existing != nil {
			token = existing.Token
		}
		if token == "" || len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
			return "", "", "", grafanaCredentials{}, fmt.Errorf("请填写有效的 Grafana Token（不能包含换行）")
		}
		creds.Token = token
	case "basic":
		username := strings.TrimSpace(input.Username)
		password := input.Password
		if username == "" && existing != nil {
			username = existing.Username
		}
		if password == "" && existing != nil {
			password = existing.Password
		}
		if username == "" || password == "" || len(username) > 256 || len(password) > 8192 || strings.ContainsAny(username+password, "\r\n") {
			return "", "", "", grafanaCredentials{}, fmt.Errorf("请填写有效的 Basic Auth 用户名与密码（不能包含换行）")
		}
		creds.Username = username
		creds.Password = password
	case "cookie":
		cookie := strings.TrimSpace(input.Cookie)
		if cookie == "" && existing != nil {
			cookie = existing.Cookie
		}
		if cookie == "" || len(cookie) > 16384 || strings.ContainsAny(cookie, "\r\n") {
			return "", "", "", grafanaCredentials{}, fmt.Errorf("请填写有效的 Grafana Cookie（不能包含换行）")
		}
		creds.Cookie = cookie
	default:
		return "", "", "", grafanaCredentials{}, fmt.Errorf("认证方式仅支持 bearer、basic 或 cookie")
	}
	return name, baseURL, origin, creds, nil
}

func applyGrafanaAuth(req *http.Request, authType string, creds grafanaCredentials) error {
	switch authType {
	case "bearer":
		if creds.Token == "" {
			return fmt.Errorf("Grafana 连接缺少 Token 凭据")
		}
		req.Header.Set("Authorization", "Bearer "+creds.Token)
	case "basic":
		if creds.Username == "" || creds.Password == "" {
			return fmt.Errorf("Grafana 连接缺少 Basic Auth 凭据")
		}
		req.SetBasicAuth(creds.Username, creds.Password)
	case "cookie":
		if creds.Cookie == "" {
			return fmt.Errorf("Grafana 连接缺少 Cookie 凭据")
		}
		req.Header.Set("Cookie", creds.Cookie)
	default:
		return fmt.Errorf("不支持的 Grafana 认证方式")
	}
	return nil
}

func (a *App) getGrafanaConnection(ctx context.Context, id int64) (grafanaConnectionRecord, error) {
	var rec grafanaConnectionRecord
	var sealed []byte
	err := a.db.QueryRow(ctx, `SELECT id,name,base_url,auth_type,credentials,created_at,updated_at FROM grafana_connections WHERE id=$1`, id).
		Scan(&rec.ID, &rec.Name, &rec.BaseURL, &rec.AuthType, &sealed, &rec.CreatedAt, &rec.UpdatedAt)
	if err != nil {
		return rec, err
	}
	plain, err := a.unseal(sealed)
	if err != nil {
		return rec, fmt.Errorf("无法解密 Grafana 连接凭据，请确认密钥未变更")
	}
	if err := json.Unmarshal(plain, &rec.Credentials); err != nil {
		return rec, fmt.Errorf("Grafana 连接凭据数据损坏")
	}
	if rec.AuthType == "basic" {
		rec.Username = rec.Credentials.Username
	}
	return rec, nil
}

func (a *App) listGrafanaConnections(ctx context.Context, includeUsername bool) ([]GrafanaConnection, error) {
	rows, err := a.db.Query(ctx, `SELECT id,name,base_url,auth_type,credentials,created_at,updated_at FROM grafana_connections ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GrafanaConnection{}
	for rows.Next() {
		var item GrafanaConnection
		var sealed []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &item.AuthType, &sealed, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if includeUsername && item.AuthType == "basic" {
			if plain, err := a.unseal(sealed); err == nil {
				var creds grafanaCredentials
				if json.Unmarshal(plain, &creds) == nil {
					item.Username = creds.Username
				}
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) checkGrafanaConnectionExists(ctx context.Context, d Definition) error {
	if !d.isGrafana() {
		return nil
	}
	var exists bool
	if err := a.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM grafana_connections WHERE id=$1)", d.Grafana.ConnectionID).Scan(&exists); err != nil {
		return fmt.Errorf("无法确认 Grafana 连接，请重试")
	}
	if !exists {
		return fmt.Errorf("所选 Grafana 连接不存在或已被删除")
	}
	return nil
}

func (a *App) toolsUsingGrafanaConnection(ctx context.Context, connID int64) ([]string, error) {
	rows, err := a.db.Query(ctx, "SELECT definition FROM tools ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		def, err := a.decrypt(data)
		if err != nil {
			continue
		}
		if def.isGrafana() && def.Grafana != nil && def.Grafana.ConnectionID == connID {
			names = append(names, def.Name)
		}
	}
	return names, rows.Err()
}

func (a *App) fetchGrafanaDatasources(ctx context.Context, conn grafanaConnectionRecord) ([]GrafanaDatasource, error) {
	baseURL, origin, err := normalizeGrafanaBaseURL(conn.BaseURL)
	if err != nil {
		return nil, err
	}
	if !a.allowed[origin] {
		return nil, fmt.Errorf("该 Grafana 地址未获准访问，请联系管理员配置 UPSTREAM_ORIGINS")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET", baseURL+"/api/datasources", nil)
	if err != nil {
		return nil, fmt.Errorf("无法构造 Grafana 数据源请求")
	}
	req.Header.Set("Accept", "application/json")
	if err := applyGrafanaAuth(req, conn.AuthType, conn.Credentials); err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if reqCtx.Err() != nil {
			return nil, fmt.Errorf("连接 Grafana 超时")
		}
		return nil, fmt.Errorf("无法连接 Grafana，请检查地址与网络连通性")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 Grafana 响应失败")
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("Grafana 数据源响应超过 2 MiB 限制")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240] + "…"
		}
		if msg == "" {
			return nil, fmt.Errorf("Grafana 返回 HTTP %d，请检查地址和认证凭据", resp.StatusCode)
		}
		return nil, fmt.Errorf("Grafana 返回 HTTP %d：%s", resp.StatusCode, msg)
	}
	var raw []struct {
		UID       string `json:"uid"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		IsDefault bool   `json:"isDefault"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("Grafana 数据源列表响应格式无效，请检查地址和认证凭据")
	}
	list := make([]GrafanaDatasource, 0, len(raw))
	for _, item := range raw {
		if item.UID == "" || item.Type == "" {
			continue
		}
		list = append(list, GrafanaDatasource{
			UID:       item.UID,
			Name:      item.Name,
			Type:      item.Type,
			IsDefault: item.IsDefault,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDefault != list[j].IsDefault {
			return list[i].IsDefault
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}

func (a *App) grafanaConnections(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if r.Method == "GET" {
		items, err := a.listGrafanaConnections(r.Context(), u.Role == "admin")
		if err != nil {
			fail(w, 500, "读取 Grafana 连接失败")
			return
		}
		writeJSON(w, 200, items)
		return
	}
	if u.Role != "admin" {
		fail(w, 403, "仅管理员可管理 Grafana 连接")
		return
	}
	var input grafanaConnectionInput
	if !decode(w, r, &input) {
		return
	}
	name, baseURL, origin, creds, err := validateGrafanaConnection(input, nil)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !a.allowed[origin] {
		fail(w, 400, "该 Grafana 地址尚未获准访问，请先配置 UPSTREAM_ORIGINS")
		return
	}
	rawCreds, err := json.Marshal(creds)
	if err != nil {
		fail(w, 500, "凭据编码失败")
		return
	}
	sealed, err := a.seal(rawCreds)
	if err != nil {
		fail(w, 500, "凭据加密失败")
		return
	}
	var item GrafanaConnection
	err = a.db.QueryRow(r.Context(),
		`INSERT INTO grafana_connections(name,base_url,auth_type,credentials) VALUES($1,$2,$3,$4)
		 RETURNING id,name,base_url,auth_type,created_at,updated_at`,
		name, baseURL, input.AuthType, sealed).
		Scan(&item.ID, &item.Name, &item.BaseURL, &item.AuthType, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if isDuplicate(err) {
			fail(w, 409, "已存在同名的 Grafana 连接")
		} else {
			fail(w, 500, "保存 Grafana 连接失败")
		}
		return
	}
	if item.AuthType == "basic" {
		item.Username = creds.Username
	}
	writeJSON(w, 201, item)
}

func (a *App) grafanaConnection(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Role != "admin" {
		fail(w, 403, "仅管理员可管理 Grafana 连接")
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if r.Method == "DELETE" {
		refs, err := a.toolsUsingGrafanaConnection(r.Context(), id)
		if err != nil {
			fail(w, 500, "检查连接引用失败")
			return
		}
		if len(refs) > 0 {
			preview := refs
			if len(preview) > 3 {
				preview = preview[:3]
			}
			fail(w, 409, fmt.Sprintf("该连接正在被 %d 个工具使用（如 %s），请先修改或删除相关工具", len(refs), strings.Join(preview, "、")))
			return
		}
		tag, err := a.db.Exec(r.Context(), "DELETE FROM grafana_connections WHERE id=$1", id)
		if err != nil {
			fail(w, 500, "删除连接失败")
			return
		}
		if tag.RowsAffected() == 0 {
			fail(w, 404, "Grafana 连接不存在")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	existing, err := a.getGrafanaConnection(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			fail(w, 404, "Grafana 连接不存在")
		} else {
			fail(w, 500, "读取 Grafana 连接失败")
		}
		return
	}
	var input grafanaConnectionInput
	if !decode(w, r, &input) {
		return
	}
	var keep *grafanaCredentials
	if input.AuthType == existing.AuthType {
		keep = &existing.Credentials
	}
	name, baseURL, origin, creds, err := validateGrafanaConnection(input, keep)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !a.allowed[origin] {
		fail(w, 400, "该 Grafana 地址尚未获准访问，请先配置 UPSTREAM_ORIGINS")
		return
	}
	rawCreds, err := json.Marshal(creds)
	if err != nil {
		fail(w, 500, "凭据编码失败")
		return
	}
	sealed, err := a.seal(rawCreds)
	if err != nil {
		fail(w, 500, "凭据加密失败")
		return
	}
	var item GrafanaConnection
	err = a.db.QueryRow(r.Context(),
		`UPDATE grafana_connections SET name=$1,base_url=$2,auth_type=$3,credentials=$4,updated_at=now() WHERE id=$5
		 RETURNING id,name,base_url,auth_type,created_at,updated_at`,
		name, baseURL, input.AuthType, sealed, id).
		Scan(&item.ID, &item.Name, &item.BaseURL, &item.AuthType, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if isDuplicate(err) {
			fail(w, 409, "已存在同名的 Grafana 连接")
		} else {
			fail(w, 500, "更新 Grafana 连接失败")
		}
		return
	}
	if item.AuthType == "basic" {
		item.Username = creds.Username
	}
	writeJSON(w, 200, item)
}

func (a *App) testGrafanaConnection(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Role != "admin" {
		fail(w, 403, "仅管理员可测试 Grafana 连接")
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	conn, err := a.getGrafanaConnection(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			fail(w, 404, "Grafana 连接不存在")
		} else {
			fail(w, 500, "读取 Grafana 连接失败")
		}
		return
	}
	ds, err := a.fetchGrafanaDatasources(r.Context(), conn)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "datasourceCount": len(ds)})
}

func (a *App) grafanaDatasources(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	conn, err := a.getGrafanaConnection(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			fail(w, 404, "Grafana 连接不存在")
		} else {
			fail(w, 500, "读取 Grafana 连接失败")
		}
		return
	}
	ds, err := a.fetchGrafanaDatasources(r.Context(), conn)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, ds)
}

func (d Definition) validateGrafana() error {
	if d.Grafana == nil {
		return fmt.Errorf("请填写 Grafana 查询配置")
	}
	if d.Grafana.ConnectionID < 1 {
		return fmt.Errorf("请选择 Grafana 连接")
	}
	if !d.Grafana.FromRequired && strings.TrimSpace(d.Grafana.DefaultFrom) == "" {
		return fmt.Errorf("开始时间（from）设为选填时，请填写默认开始时间（如 now-1h）")
	}
	if !d.Grafana.ToRequired && strings.TrimSpace(d.Grafana.DefaultTo) == "" {
		return fmt.Errorf("结束时间（to）设为选填时，请填写默认结束时间（如 now）")
	}
	if len(d.Grafana.DefaultFrom) > 128 || len(d.Grafana.DefaultTo) > 128 {
		return fmt.Errorf("默认时间值长度不能超过 128 字节")
	}
	if len(d.Grafana.Queries) == 0 || len(d.Grafana.Queries) > 20 {
		return fmt.Errorf("请配置 1–20 条查询语句")
	}

	seen := map[string]bool{}
	paramByName := map[string]Parameter{}
	for _, p := range d.Parameters {
		lower := strings.ToLower(p.Name)
		if !parameterName.MatchString(p.Name) || seen[lower] {
			return fmt.Errorf("参数名称无效或重复：%s", p.Name)
		}
		if lower == "from" || lower == "to" {
			return fmt.Errorf("from 和 to 为内置时间参数，请勿重复添加到参数表")
		}
		seen[lower] = true
		if len(p.Value) > 32768 || len(p.Description) > 2000 {
			return fmt.Errorf("参数内容过长：%s", p.Name)
		}
		switch p.Type {
		case "string", "boolean", "integer", "number":
		case "array", "object":
			return fmt.Errorf("Grafana 查询语句占位符参数不支持数组或对象类型：%s", p.Name)
		default:
			return fmt.Errorf("参数类型无效：%s", p.Name)
		}
		if p.NonEmpty && p.Fixed {
			return fmt.Errorf("仅客户端填写的参数可设置非空：%s", p.Name)
		}
		if p.QueryFilter && p.Fixed {
			return fmt.Errorf("仅客户端填写的参数可设为筛选参数：%s", p.Name)
		}
		if p.In != "" && p.In != "query" {
			return fmt.Errorf("Grafana 查询参数位置无效：%s", p.Name)
		}
		if p.Fixed {
			if _, err := fixedValue(p); err != nil {
				return err
			}
		}
		paramByName[p.Name] = p
	}

	refSeen := map[string]bool{}
	usedParams := map[string]bool{}
	for i, q := range d.Grafana.Queries {
		refID := strings.TrimSpace(q.RefID)
		if !grafanaRefIDPattern.MatchString(refID) || refSeen[strings.ToLower(refID)] {
			return fmt.Errorf("第 %d 条查询的标识（RefID）无效或重复：%s", i+1, q.RefID)
		}
		refSeen[strings.ToLower(refID)] = true
		if strings.TrimSpace(q.DatasourceUID) == "" || strings.TrimSpace(q.DatasourceType) == "" {
			return fmt.Errorf("查询 %s 请选择有效的数据源", refID)
		}
		if strings.TrimSpace(q.Expr) == "" || len(q.Expr) > 32768 {
			return fmt.Errorf("查询 %s 请输入不超过 32768 字节的查询语句", refID)
		}
		if q.MaxDataPoints < 0 || q.MaxDataPoints > 100000 {
			return fmt.Errorf("查询 %s 的最大点数须在 0–100000 之间", refID)
		}
		if q.MaxLines < 0 || q.MaxLines > 10000 {
			return fmt.Errorf("查询 %s 的最大条数须在 0–10000 之间", refID)
		}
		if len(q.Interval) > 64 || len(q.TimeField) > 128 {
			return fmt.Errorf("查询 %s 的步长或时间字段配置过长", refID)
		}
		if grafanaEmptyVarCheck.MatchString(q.Expr) {
			return fmt.Errorf("查询 %s 包含空占位符 {{}}", refID)
		}
		for _, m := range grafanaVarPattern.FindAllStringSubmatch(q.Expr, -1) {
			name := m[1]
			if name == "from" || name == "to" {
				continue
			}
			if _, ok := paramByName[name]; !ok {
				return fmt.Errorf("查询 %s 引用了未配置的参数占位符：%s", refID, name)
			}
			usedParams[name] = true
		}
	}
	for _, p := range d.Parameters {
		if !usedParams[p.Name] {
			return fmt.Errorf("参数 %s 未在任何查询语句的 {{%s}} 占位符中使用", p.Name, p.Name)
		}
	}
	if err := d.Approval.validate(); err != nil {
		return err
	}
	return validateOutputFields(d.OutputFields)
}

func (d Definition) grafanaInputSchema() map[string]any {
	properties := map[string]any{}
	required := []string{}
	fromDesc := "查询开始时间，支持相对时间（如 now-1h）、ISO8601 或 Unix 毫秒时间戳"
	if d.Grafana != nil && !d.Grafana.FromRequired && strings.TrimSpace(d.Grafana.DefaultFrom) != "" {
		fromDesc += "。不传时默认：" + strings.TrimSpace(d.Grafana.DefaultFrom)
	}
	fromSchema := map[string]any{"type": "string", "description": fromDesc}
	if d.Grafana != nil && d.Grafana.FromRequired {
		fromSchema["pattern"] = `\S`
		required = append(required, "from")
	} else if d.Grafana != nil && strings.TrimSpace(d.Grafana.DefaultFrom) != "" {
		fromSchema["default"] = strings.TrimSpace(d.Grafana.DefaultFrom)
	}
	properties["from"] = fromSchema

	toDesc := "查询结束时间，支持相对时间（如 now）、ISO8601 或 Unix 毫秒时间戳"
	if d.Grafana != nil && !d.Grafana.ToRequired && strings.TrimSpace(d.Grafana.DefaultTo) != "" {
		toDesc += "。不传时默认：" + strings.TrimSpace(d.Grafana.DefaultTo)
	}
	toSchema := map[string]any{"type": "string", "description": toDesc}
	if d.Grafana != nil && d.Grafana.ToRequired {
		toSchema["pattern"] = `\S`
		required = append(required, "to")
	} else if d.Grafana != nil && strings.TrimSpace(d.Grafana.DefaultTo) != "" {
		toSchema["default"] = strings.TrimSpace(d.Grafana.DefaultTo)
	}
	properties["to"] = toSchema

	for _, p := range d.Parameters {
		if p.Fixed {
			continue
		}
		properties[p.Name] = p.schema()
		if p.requiresValue() {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	if filters := d.queryFilterNames(); len(filters) > 0 {
		schema["description"] = queryFilterMessage(filters)
	}
	return schema
}

func resolveGrafanaTimeArg(name string, required bool, defaultVal string, args map[string]any) (string, error) {
	raw, exists := args[name]
	if exists {
		s, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("参数 %s 的类型应为 string", name)
		}
		s = strings.TrimSpace(s)
		if s != "" {
			return s, nil
		}
	}
	if required {
		return "", fmt.Errorf("缺少必填时间参数：%s", name)
	}
	def := strings.TrimSpace(defaultVal)
	if def == "" {
		return "", fmt.Errorf("缺少时间参数 %s 且未配置默认值", name)
	}
	return def, nil
}

func (d Definition) buildGrafanaRequest(conn grafanaConnectionRecord, args map[string]any) (*http.Request, string, string, error) {
	if err := d.Validate(); err != nil {
		return nil, "", "", err
	}
	baseURL, _, err := normalizeGrafanaBaseURL(conn.BaseURL)
	if err != nil {
		return nil, "", "", err
	}
	dynamic := map[string]Parameter{}
	for _, p := range d.Parameters {
		if !p.Fixed {
			dynamic[p.Name] = p
		}
	}
	for name := range args {
		if name == "from" || name == "to" {
			continue
		}
		if _, ok := dynamic[name]; !ok {
			return nil, "", "", fmt.Errorf("不允许传入未知或固定参数：%s", name)
		}
	}
	resolvedFrom, err := resolveGrafanaTimeArg("from", d.Grafana.FromRequired, d.Grafana.DefaultFrom, args)
	if err != nil {
		return nil, "", "", err
	}
	resolvedTo, err := resolveGrafanaTimeArg("to", d.Grafana.ToRequired, d.Grafana.DefaultTo, args)
	if err != nil {
		return nil, "", "", err
	}

	filters := d.queryFilterNames()
	values := map[string]string{
		"from": resolvedFrom,
		"to":   resolvedTo,
	}
	for _, p := range d.Parameters {
		value, exists := args[p.Name]
		if p.Fixed {
			value, _ = fixedValue(p)
		} else if exists {
			if !validType(value, p.Type) {
				if p.Type == "integer" {
					return nil, "", "", fmt.Errorf("参数 %s 应为安全范围内的整数；超过 ±9007199254740991 的 ID 请使用字符串类型", p.Name)
				}
				return nil, "", "", fmt.Errorf("参数 %s 的类型应为 %s", p.Name, p.Type)
			}
			optionalFilter := p.QueryFilter && !p.requiresValue()
			if optionalFilter && !hasQueryFilterValue(value) {
				exists = false
			}
			if p.NonEmpty {
				if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
					return nil, "", "", fmt.Errorf("参数 %s 不能为空白", p.Name)
				}
			}
		}
		if !p.Fixed && !exists {
			if p.NonEmpty {
				return nil, "", "", fmt.Errorf("参数 %s 不能为空", p.Name)
			}
			if p.requiresValue() {
				return nil, "", "", fmt.Errorf("缺少必填参数：%s", p.Name)
			}
			values[p.Name] = ""
			continue
		}
		values[p.Name] = valueText(value)
	}
	if len(filters) > 0 && !slices.ContainsFunc(filters, func(name string) bool {
		return hasQueryFilterValue(args[name])
	}) {
		return nil, "", "", fmt.Errorf("%s", queryFilterMessage(filters))
	}

	queries := make([]map[string]any, 0, len(d.Grafana.Queries))
	for _, q := range d.Grafana.Queries {
		rendered := grafanaVarPattern.ReplaceAllStringFunc(q.Expr, func(match string) string {
			name := match[2 : len(match)-2]
			return values[name]
		})
		queries = append(queries, buildGrafanaQueryPayload(q, rendered))
	}
	payload, err := json.Marshal(map[string]any{
		"from":    resolvedFrom,
		"to":      resolvedTo,
		"queries": queries,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("构造 Grafana 查询请求失败")
	}
	req, err := http.NewRequest("POST", baseURL+"/api/ds/query", bytes.NewReader(payload))
	if err != nil {
		return nil, "", "", fmt.Errorf("无法创建 Grafana 请求")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := applyGrafanaAuth(req, conn.AuthType, conn.Credentials); err != nil {
		return nil, "", "", err
	}
	return req, resolvedFrom, resolvedTo, nil
}

func grafanaDatasourceFamily(dsType string) string {
	t := strings.ToLower(strings.TrimSpace(dsType))
	switch {
	case strings.Contains(t, "victoriametrics-logs") || strings.Contains(t, "victorialogs") || strings.Contains(t, "vmlogs"):
		return "victorialogs"
	case strings.Contains(t, "loki"):
		return "loki"
	case strings.Contains(t, "prometheus") || strings.Contains(t, "victoriametrics"):
		return "prometheus"
	case strings.Contains(t, "clickhouse"):
		return "clickhouse"
	case strings.Contains(t, "mysql") || strings.Contains(t, "postgres") || strings.Contains(t, "mssql") || strings.Contains(t, "sqlite") || strings.Contains(t, "trino") || strings.Contains(t, "oracle") || strings.Contains(t, "snowflake"):
		return "sql"
	case strings.Contains(t, "elasticsearch") || strings.Contains(t, "opensearch"):
		return "elasticsearch"
	default:
		return "generic"
	}
}

func buildGrafanaQueryPayload(q GrafanaQuery, renderedExpr string) map[string]any {
	ds := map[string]any{
		"uid":  strings.TrimSpace(q.DatasourceUID),
		"type": strings.TrimSpace(q.DatasourceType),
	}
	item := map[string]any{
		"refId":      strings.TrimSpace(q.RefID),
		"datasource": ds,
	}
	switch grafanaDatasourceFamily(q.DatasourceType) {
	case "prometheus":
		instant := q.QueryMode == "instant"
		item["expr"] = renderedExpr
		item["instant"] = instant
		item["range"] = !instant
		if q.Interval != "" {
			item["interval"] = strings.TrimSpace(q.Interval)
		}
		if q.MaxDataPoints > 0 {
			item["maxDataPoints"] = q.MaxDataPoints
		}
	case "loki":
		mode := q.QueryMode
		if mode != "instant" && mode != "range" {
			mode = "range"
		}
		maxLines := q.MaxLines
		if maxLines <= 0 {
			maxLines = 100
		}
		item["expr"] = renderedExpr
		item["queryType"] = mode
		item["maxLines"] = maxLines
		if q.Interval != "" {
			item["interval"] = strings.TrimSpace(q.Interval)
		}
	case "victorialogs":
		mode := q.QueryMode
		switch mode {
		case "instant", "stats", "statsRange", "hits":
		default:
			mode = "instant"
		}
		maxLines := q.MaxLines
		if maxLines <= 0 {
			maxLines = 100
		}
		item["expr"] = renderedExpr
		item["queryType"] = mode
		item["maxLines"] = maxLines
		if q.Interval != "" {
			item["step"] = strings.TrimSpace(q.Interval)
			item["interval"] = strings.TrimSpace(q.Interval)
		}
	case "clickhouse":
		formatCode := 1 // 1 = Table, 0 = TimeSeries
		if q.QueryMode == "time_series" {
			formatCode = 0
		}
		item["rawSql"] = renderedExpr
		item["queryType"] = "sql"
		item["format"] = formatCode
	case "sql":
		format := q.QueryMode
		if format != "table" && format != "time_series" {
			format = "table"
		}
		item["rawSql"] = renderedExpr
		item["rawQuery"] = true
		item["format"] = format
	case "elasticsearch":
		metricType := q.QueryMode
		if metricType != "raw_data" && metricType != "logs" {
			metricType = "raw_data"
		}
		size := q.MaxLines
		if size <= 0 {
			size = 100
		}
		item["query"] = renderedExpr
		item["metrics"] = []map[string]any{
			{"id": "1", "type": metricType, "settings": map[string]any{"size": strconv.Itoa(size)}},
		}
		item["bucketAggs"] = []any{}
		if strings.TrimSpace(q.TimeField) != "" {
			item["timeField"] = strings.TrimSpace(q.TimeField)
		}
	default:
		item["expr"] = renderedExpr
	}
	return item
}

type grafanaDSResponse struct {
	Results map[string]grafanaRefResult `json:"results"`
}

type grafanaRefResult struct {
	Status int            `json:"status"`
	Error  string         `json:"error"`
	Frames []grafanaFrame `json:"frames"`
}

type grafanaFrame struct {
	Schema struct {
		Name   string `json:"name"`
		RefID  string `json:"refId"`
		Fields []struct {
			Name   string            `json:"name"`
			Type   string            `json:"type"`
			Labels map[string]string `json:"labels"`
		} `json:"fields"`
		Meta struct {
			Notices []struct {
				Severity string `json:"severity"`
				Text     string `json:"text"`
			} `json:"notices"`
		} `json:"meta"`
	} `json:"schema"`
	Data struct {
		Values [][]any `json:"values"`
	} `json:"data"`
}

type formattedGrafanaResponse struct {
	From    string                           `json:"from"`
	To      string                           `json:"to"`
	Results map[string]formattedGrafanaEntry `json:"results"`
}

type formattedGrafanaEntry struct {
	Status int                    `json:"status,omitempty"`
	Error  string                 `json:"error,omitempty"`
	Frames []formattedGrafanaItem `json:"frames"`
}

type formattedGrafanaItem struct {
	Name     string            `json:"name,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Notices  []string          `json:"notices,omitempty"`
	RowCount int               `json:"rowCount"`
	Rows     []map[string]any  `json:"rows"`
}

func formatGrafanaValue(fieldType string, v any) any {
	if fieldType != "time" || v == nil {
		return v
	}
	switch n := v.(type) {
	case json.Number:
		if ms, err := n.Int64(); err == nil {
			return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
		}
	case float64:
		return time.UnixMilli(int64(n)).UTC().Format(time.RFC3339Nano)
	}
	return v
}

func formatGrafanaResponse(from, to string, raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var parsed grafanaDSResponse
	if err := dec.Decode(&parsed); err != nil || parsed.Results == nil {
		return raw, nil
	}
	out := formattedGrafanaResponse{
		From:    from,
		To:      to,
		Results: make(map[string]formattedGrafanaEntry, len(parsed.Results)),
	}
	for refID, res := range parsed.Results {
		entry := formattedGrafanaEntry{
			Status: res.Status,
			Error:  res.Error,
			Frames: make([]formattedGrafanaItem, 0, len(res.Frames)),
		}
		for _, frame := range res.Frames {
			fields := frame.Schema.Fields
			cols := frame.Data.Values
			rowCount := 0
			for _, col := range cols {
				if len(col) > rowCount {
					rowCount = len(col)
				}
			}
			names := make([]string, len(fields))
			used := map[string]int{}
			mergedLabels := map[string]string{}
			for i, f := range fields {
				base := f.Name
				if base == "" {
					base = fmt.Sprintf("col_%d", i+1)
				}
				used[base]++
				if used[base] > 1 {
					names[i] = fmt.Sprintf("%s_%d", base, used[base])
				} else {
					names[i] = base
				}
				for k, v := range f.Labels {
					mergedLabels[k] = v
				}
			}
			rows := make([]map[string]any, 0, rowCount)
			for r := 0; r < rowCount; r++ {
				row := make(map[string]any, len(fields))
				for c, f := range fields {
					var val any
					if c < len(cols) && r < len(cols[c]) {
						val = formatGrafanaValue(f.Type, cols[c][r])
					}
					row[names[c]] = val
				}
				rows = append(rows, row)
			}
			var notices []string
			for _, notice := range frame.Schema.Meta.Notices {
				if strings.TrimSpace(notice.Text) != "" {
					notices = append(notices, notice.Text)
				}
			}
			item := formattedGrafanaItem{
				Name:     frame.Schema.Name,
				Notices:  notices,
				RowCount: len(rows),
				Rows:     rows,
			}
			if len(mergedLabels) > 0 {
				item.Labels = mergedLabels
			}
			entry.Frames = append(entry.Frames, item)
		}
		out.Results[refID] = entry
	}
	return json.Marshal(out)
}
