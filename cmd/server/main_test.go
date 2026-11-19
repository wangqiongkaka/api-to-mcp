package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitEnvPreservesSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := initEnv(path); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = initEnv(path); err == nil {
		t.Fatal("不应覆盖已有配置")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("原有配置被修改")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("配置文件权限不安全")
	}
	if !strings.Contains(string(before), "ENCRYPTION_KEY=") || !strings.Contains(string(before), "ADMIN_PASSWORD=") {
		t.Fatal("初始化配置不完整")
	}
}
