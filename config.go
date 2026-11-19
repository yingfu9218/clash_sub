package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

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

	LoginAllowIPs  []string `yaml:"login_allow_ips"`
	TrustedProxies []string `yaml:"trusted_proxies"`

	// loginAllow is LoginAllowIPs parsed once at startup. Empty means anyone
	// may attempt to log in.
	loginAllow []netip.Prefix
}

const configTemplate = `# clash-sub 配置
#
# password_hash 是 bcrypt 哈希，不是明文密码。
# 要改密码，把 password_hash 整行删掉再重启：程序会生成一个新密码并打印到控制台。
# secret 用来签名登录 cookie，改掉它会让所有已登录的会话立即失效。
#
# login_allow_ips 是允许登录的 IP / 网段，例如 ["1.2.3.4", "10.0.0.0/8", "2001:db8::/32"]。
# 留空 [] 表示不限制。被拒绝的 IP 会打印到日志里，照着加进来即可。
#
# trusted_proxies 是前置反向代理（Nginx / Caddy）的地址。只有来自这些地址的
# X-Forwarded-For 才会被采信，否则一律按 TCP 连接的对端地址算。
# 直接暴露在公网就留空 []；Nginx 在本机就填 ["127.0.0.1", "::1"]。

listen: %q
username: %q
password_hash: %q
secret: %q
data_file: %q
login_allow_ips: %s
trusted_proxies: %s
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

	// A config written by an older build lacks the newer keys. Rewriting it
	// puts them, and the template's comments explaining them, in front of the
	// operator.
	dirty := false
	if err == nil {
		var keys map[string]any
		if yaml.Unmarshal(raw, &keys) == nil {
			for _, k := range []string{"login_allow_ips", "trusted_proxies"} {
				if _, ok := keys[k]; !ok {
					dirty = true
				}
			}
		}
	}

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

	for _, raw := range cfg.LoginAllowIPs {
		p, err := parsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("login_allow_ips 里的 %q 不是合法的 IP 或网段", raw)
		}
		cfg.loginAllow = append(cfg.loginAllow, p)
	}
	for _, raw := range cfg.TrustedProxies {
		if _, err := parsePrefix(raw); err != nil {
			return nil, fmt.Errorf("trusted_proxies 里的 %q 不是合法的 IP 或网段", raw)
		}
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
	body := fmt.Sprintf(configTemplate, c.Listen, c.Username, c.PasswordHash, c.Secret, c.DataFile,
		yamlList(c.LoginAllowIPs), yamlList(c.TrustedProxies))
	// 0600: the file holds a password hash and the cookie signing secret.
	return os.WriteFile(path, []byte(body), 0o600)
}

// yamlList renders a flow sequence, so an empty list still shows the operator
// the key exists.
func yamlList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = fmt.Sprintf("%q", it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// parsePrefix accepts a bare IP as a single-address prefix. IPv4-mapped IPv6
// is unmapped so "1.2.3.4" also matches a dual-stack listener's "::ffff:1.2.3.4".
func parsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// loginAllowed reports whether ip may attempt to log in.
func (c *Config) loginAllowed(ip string) bool {
	if len(c.loginAllow) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range c.loginAllow {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
