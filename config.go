package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// Config is the operator-facing file. It is created on first run with a
// generated password, which is printed once to stdout.
type Config struct {
	Listen       string `yaml:"listen"`
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
	Secret       string `yaml:"secret"`
	DataFile     string `yaml:"data_file"`
}

const configTemplate = `# clash-sub 配置
#
# password_hash 是 bcrypt 哈希，不是明文密码。
# 要改密码，把 password_hash 整行删掉再重启：程序会生成一个新密码并打印到控制台。
# secret 用来签名登录 cookie，改掉它会让所有已登录的会话立即失效。

listen: %q
username: %q
password_hash: %q
secret: %q
data_file: %q
`

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("no entropy available: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// LoadConfig reads path, creating it if absent. It also repairs a config whose
// password_hash was deleted by the operator, which is the documented way to
// reset a forgotten password.
func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		Listen:   ":8088",
		Username: "admin",
		DataFile: "data/subscriptions.json",
	}

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("解析 %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// fall through to generation below
	default:
		return nil, err
	}

	dirty := false

	if cfg.Listen == "" {
		cfg.Listen = ":8088"
		dirty = true
	}
	if cfg.Username == "" {
		cfg.Username = "admin"
		dirty = true
	}
	if cfg.DataFile == "" {
		cfg.DataFile = "data/subscriptions.json"
		dirty = true
	}
	if cfg.Secret == "" {
		cfg.Secret = randomHex(32)
		dirty = true
	}

	if cfg.PasswordHash == "" {
		pw := randomHex(9) // 18 hex chars
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		cfg.PasswordHash = string(hash)
		dirty = true

		fmt.Println()
		fmt.Println("  ┌─ 已生成登录密码 ─────────────────────────────")
		fmt.Printf("  │  用户名  %s\n", cfg.Username)
		fmt.Printf("  │  密码    %s\n", pw)
		fmt.Println("  │")
		fmt.Println("  │  这串密码只显示这一次，请立刻记下来。")
		fmt.Printf("  │  忘记了就删掉 %s 里的 password_hash 再重启。\n", path)
		fmt.Println("  └──────────────────────────────────────────────")
		fmt.Println()
	}

	if dirty {
		if err := writeConfig(path, cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func writeConfig(path string, c *Config) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	body := fmt.Sprintf(configTemplate, c.Listen, c.Username, c.PasswordHash, c.Secret, c.DataFile)
	// 0600: the file holds a password hash and the cookie signing secret.
	return os.WriteFile(path, []byte(body), 0o600)
}
