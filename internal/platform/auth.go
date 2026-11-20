package platform

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type userKey struct{}
type attempt struct {
	Count int
	Until time.Time
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{2,31}$`)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimited(w, r, v, 256<<10)
}

func decodeLimited(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "请求 JSON 格式无效或包含未支持的字段")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, 400, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func currentUser(r *http.Request) User { return r.Context().Value(userKey{}).(User) }

func (a *App) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("api_mcp_session")
		if err != nil || len(cookie.Value) > 128 {
			fail(w, 401, "请先登录")
			return
		}
		var u User
		err = a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.role,u.disabled FROM sessions s
 JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at > now() AND NOT u.disabled`, hashToken(cookie.Value)).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled)
		if err != nil {
			if isNotFound(err) {
				fail(w, 401, "登录已失效，请重新登录")
			} else {
				fail(w, 503, "身份验证暂时不可用")
			}
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}

func (a *App) auth(w http.ResponseWriter, r *http.Request) {
	if !a.allowAuth(r.RemoteAddr) {
		fail(w, 429, "尝试过于频繁，请一分钟后再试")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 12 || len(input.Password) > 72 {
		fail(w, 400, "用户名须为 3–32 位字母、数字、下划线或连字符且以字母开头；密码须为 12–72 字节")
		return
	}
	var u User
	var hash string
	if r.URL.Path == "/api/auth/register" {
		b, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
		if err != nil {
			fail(w, 500, "密码处理失败")
			return
		}
		err = a.db.QueryRow(r.Context(), "INSERT INTO users(username,password_hash,role) VALUES($1,$2,'member') RETURNING id,username,role,disabled", input.Username, string(b)).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled)
		if err != nil {
			if isDuplicate(err) {
				fail(w, 409, "用户名已存在")
			} else {
				fail(w, 500, "创建账户失败")
			}
			return
		}
	} else {
		err := a.db.QueryRow(r.Context(), "SELECT id,username,role,disabled,password_hash FROM users WHERE username=$1", input.Username).Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &hash)
		if err != nil && !isNotFound(err) {
			fail(w, 503, "登录暂时不可用")
			return
		}
		if err != nil {
			hash = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"
		}
		passwordErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password))
		if err != nil || u.Disabled || passwordErr != nil {
			fail(w, 401, "用户名或密码错误，或账户已被停用")
			return
		}
	}
	token := randomToken()
	_, err := a.db.Exec(r.Context(), "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", hashToken(token), u.ID, time.Now().Add(24*time.Hour))
	if err != nil {
		fail(w, 500, "创建登录会话失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "api_mcp_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	writeJSON(w, 200, u)
}

func (a *App) allowAuth(remote string) bool {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	a.limitMu.Lock()
	defer a.limitMu.Unlock()
	now := time.Now()
	for key, item := range a.loginAttempts {
		if now.After(item.Until) {
			delete(a.loginAttempts, key)
		}
	}
	item := a.loginAttempts[ip]
	if item.Until.IsZero() {
		item.Until = now.Add(time.Minute)
	}
	if item.Count >= 10 || len(a.loginAttempts) >= 10000 {
		return false
	}
	item.Count++
	// shortcut: 登录限流按进程内 IP 计数，多实例部署时改用共享限流。
	a.loginAttempts[ip] = item
	return true
}

func isDuplicate(err error) bool {
	var p *pgconn.PgError
	return errors.As(err, &p) && p.Code == "23505"
}

func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}
