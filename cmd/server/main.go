package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"api-to-mcp/internal/platform"
)

func main() {
	initialize := flag.Bool("init", false, "生成本地 .env 文件，不覆盖已有配置")
	flag.Parse()
	if *initialize {
		if err := initEnv(".env"); err != nil {
			log.Fatal(err)
		}
		fmt.Println("已创建 .env（权限 0600）。初始管理员密码保存在 ADMIN_PASSWORD；请妥善保管配置文件。")
		return
	}
	if err := loadEnv(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	a, err := platform.New(startup, platform.Config{DatabaseURL: os.Getenv("DATABASE_URL"), EncryptionKey: os.Getenv("ENCRYPTION_KEY"), AdminUsername: env("ADMIN_USERNAME", "admin"), AdminPassword: os.Getenv("ADMIN_PASSWORD"), AllowedOrigins: strings.Split(os.Getenv("UPSTREAM_ORIGINS"), ","), SecureCookies: os.Getenv("COOKIE_SECURE") == "true", WebDist: env("WEB_DIST", "web/dist"), FeishuWebhookURL: os.Getenv("FEISHU_WEBHOOK_URL"), FeishuWebhookSecret: os.Getenv("FEISHU_WEBHOOK_SECRET"), PublicURL: os.Getenv("PUBLIC_URL")})
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	server := &http.Server{Addr: env("APP_ADDR", "127.0.0.1:8088"), Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("API-to-MCP 已启动：http://%s", server.Addr)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf(".env 行格式无效")
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if _, ok := os.LookupEnv(key); !ok {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return s.Err()
}

func initEnv(path string) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	password := make([]byte, 18)
	if _, err := rand.Read(password); err != nil {
		return err
	}
	dbPassword := make([]byte, 18)
	if _, err := rand.Read(dbPassword); err != nil {
		return err
	}
	data := fmt.Sprintf("APP_ADDR=127.0.0.1:8088\nDB_PASSWORD=%s\nDATABASE_URL=postgres://api_mcp:%s@127.0.0.1:55432/api_to_mcp?sslmode=disable\nENCRYPTION_KEY=%s\nADMIN_USERNAME=admin\nADMIN_PASSWORD=%s\nUPSTREAM_ORIGINS=http://10.12.72.42:9007\nCOOKIE_SECURE=false\nWEB_DIST=web/dist\n", hex.EncodeToString(dbPassword), hex.EncodeToString(dbPassword), base64.StdEncoding.EncodeToString(key), hex.EncodeToString(password))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("创建配置失败（不会覆盖已有文件）：%w", err)
	}
	if _, err = f.WriteString(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
