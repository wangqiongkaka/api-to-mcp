package platform

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	DatabaseURL    string
	EncryptionKey  string
	AdminUsername  string
	AdminPassword  string
	AllowedOrigins []string
	SecureCookies  bool
	WebDist        string
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

type ToolRecord struct {
	ID        int64     `json:"id"`
	OwnerID   int64     `json:"ownerId"`
	OwnerName string    `json:"ownerName"`
	Published bool      `json:"published"`
	UpdatedAt time.Time `json:"updatedAt"`
	Definition
}

type App struct {
	db            *pgxpool.Pool
	cipher        cipher.AEAD
	allowed       map[string]bool
	secureCookies bool
	webDist       string
	client        *http.Client
	limitMu       sync.Mutex
	loginAttempts map[string]attempt
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
 id BIGSERIAL PRIMARY KEY, username TEXT UNIQUE NOT NULL,
 password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','member')),
 disabled BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
 token_hash BYTEA PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS tools (
 id BIGSERIAL PRIMARY KEY, owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL, definition BYTEA NOT NULL,
 published BOOLEAN NOT NULL DEFAULT false, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner_id,name)
);
CREATE TABLE IF NOT EXISTS access_tokens (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL, token_hash BYTEA UNIQUE NOT NULL, prefix TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS tools_owner_idx ON tools(owner_id);
CREATE INDEX IF NOT EXISTS access_tokens_user_idx ON access_tokens(user_id);
`

func New(ctx context.Context, cfg Config) (*App, error) {
	key, err := base64.StdEncoding.DecodeString(cfg.EncryptionKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY 必须是 32 字节密钥的 Base64 编码")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, value := range cfg.AllowedOrigins {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("UPSTREAM_ORIGINS 只能包含完整的 HTTP/HTTPS 源地址")
		}
		allowed[u.Scheme+"://"+strings.ToLower(u.Host)] = true
	}
	cfg.AdminUsername = strings.ToLower(strings.TrimSpace(cfg.AdminUsername))
	if !usernamePattern.MatchString(cfg.AdminUsername) || len(cfg.AdminPassword) < 12 || len(cfg.AdminPassword) > 72 {
		return nil, fmt.Errorf("初始管理员用户名无效或密码长度不在 12–72 字节之间")
	}
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("数据库配置无效")
	}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 失败：%w", err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, schema); err != nil {
		return nil, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", cfg.AdminUsername).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), 12)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO users(username,password_hash,role) VALUES($1,$2,'admin')", cfg.AdminUsername, string(hash)); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, "DELETE FROM sessions WHERE expires_at < now()"); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	a := &App{db: db, cipher: aead, allowed: allowed, secureCookies: cfg.SecureCookies, webDist: cfg.WebDist,
		loginAttempts: map[string]attempt{}, client: &http.Client{Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	ok = true
	return a, nil
}

func (a *App) Close() { a.client.CloseIdleConnections(); a.db.Close() }

func randomToken() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("安全随机数生成失败")
	}
	return b
}

func hashToken(token string) []byte { sum := sha256.Sum256([]byte(token)); return sum[:] }

func (a *App) encrypt(d Definition) ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	nonce := randomBytes(a.cipher.NonceSize())
	return a.cipher.Seal(nonce, nonce, data, nil), nil
}

func (a *App) decrypt(data []byte) (Definition, error) {
	var d Definition
	n := a.cipher.NonceSize()
	if len(data) < n {
		return d, errors.New("接口配置损坏")
	}
	plain, err := a.cipher.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return d, errors.New("无法解密接口配置，请确认密钥未变更")
	}
	err = json.Unmarshal(plain, &d)
	return d, err
}

func (a *App) listTools(ctx context.Context, user User, ownOnly bool) ([]ToolRecord, error) {
	rows, err := a.db.Query(ctx, `SELECT t.id,t.owner_id,u.username,t.published,t.updated_at,t.definition
 FROM tools t JOIN users u ON u.id=t.owner_id WHERE ($1 AND NOT $2) OR t.owner_id=$3 ORDER BY t.updated_at DESC,t.id DESC`, user.Role == "admin", ownOnly, user.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tools := []ToolRecord{}
	for rows.Next() {
		var record ToolRecord
		var data []byte
		if err := rows.Scan(&record.ID, &record.OwnerID, &record.OwnerName, &record.Published, &record.UpdatedAt, &data); err != nil {
			return nil, err
		}
		record.Definition, err = a.decrypt(data)
		if err != nil {
			return nil, err
		}
		tools = append(tools, record)
	}
	return tools, rows.Err()
}

func (a *App) getTool(ctx context.Context, id int64, user User) (ToolRecord, error) {
	var record ToolRecord
	var data []byte
	err := a.db.QueryRow(ctx, `SELECT t.id,t.owner_id,u.username,t.published,t.updated_at,t.definition
 FROM tools t JOIN users u ON u.id=t.owner_id WHERE t.id=$1 AND (t.owner_id=$2 OR $3)`, id, user.ID, user.Role == "admin").Scan(&record.ID, &record.OwnerID, &record.OwnerName, &record.Published, &record.UpdatedAt, &data)
	if err != nil {
		return record, err
	}
	record.Definition, err = a.decrypt(data)
	return record, err
}

func isNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
