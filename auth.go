package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName    = "clash_sub_session"
	sessionTTL    = 12 * time.Hour
	rememberTTL   = 30 * 24 * time.Hour
	maxLoginFails = 5
	lockoutWindow = 15 * time.Minute
)

var b64 = base64.RawURLEncoding

// issue builds "<user>|<expiry>.<hmac>". Stateless on purpose: restarting the
// binary must not log the operator out, and there is no session table to grow.
func issue(secret []byte, user string, ttl time.Duration) string {
	payload := fmt.Sprintf("%s|%d", user, time.Now().Add(ttl).Unix())
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return b64.EncodeToString([]byte(payload)) + "." + b64.EncodeToString(mac.Sum(nil))
}

func verify(secret []byte, tok string) (string, bool) {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	payload, err := b64.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	sig, err := b64.DecodeString(parts[1])
	if err != nil {
		return "", false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}

	fields := strings.SplitN(string(payload), "|", 2)
	if len(fields) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return fields[0], true
}

// throttle slows down password guessing against an internet-exposed panel.
type throttle struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

func newThrottle() *throttle { return &throttle{fail: map[string][]time.Time{}} }

func (t *throttle) locked(ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.recent(ip)) >= maxLoginFails
}

func (t *throttle) record(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fail[ip] = append(t.recent(ip), time.Now())
}

func (t *throttle) clear(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fail, ip)
}

// recent drops attempts older than the window. Caller holds the lock.
func (t *throttle) recent(ip string) []time.Time {
	cutoff := time.Now().Add(-lockoutWindow)
	kept := t.fail[ip][:0]
	for _, at := range t.fail[ip] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	t.fail[ip] = kept
	return kept
}

func clientIP(c *gin.Context) string {
	ip := c.ClientIP()
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
	return ip
}

func (s *Server) setSession(c *gin.Context, user string, remember bool) {
	ttl := sessionTTL
	maxAge := 0 // browser session cookie
	if remember {
		ttl = rememberTTL
		maxAge = int(rememberTTL / time.Second)
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieName,
		Value:    issue(s.secret, user, ttl),
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSession(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// requireAuth guards /api. It answers JSON, never a redirect, so the frontend
// can decide where to send the person.
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok, err := c.Cookie(cookieName)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			return
		}
		user, ok := verify(s.secret, tok)
		if !ok || user != s.cfg.Username {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "登录已过期，请重新登录"})
			return
		}
		c.Set("user", user)
		c.Next()
	}
}

func (s *Server) handleLogin(c *gin.Context) {
	// Checked before anything else: an address outside the allowlist never
	// reaches bcrypt and never touches the throttle table.
	ip := clientIP(c)
	if !s.cfg.loginAllowed(ip) {
		log.Printf("拒绝登录：%s 不在 login_allow_ips 中", ip)
		c.JSON(http.StatusForbidden, gin.H{"error": "当前 IP 不允许登录"})
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}

	if s.throttle.locked(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试次数过多，请 15 分钟后再试"})
		return
	}

	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(s.cfg.Username)) == 1
	passOK := bcrypt.CompareHashAndPassword([]byte(s.cfg.PasswordHash), []byte(req.Password)) == nil

	if !userOK || !passOK {
		s.throttle.record(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码不正确"})
		return
	}

	s.throttle.clear(ip)
	s.setSession(c, s.cfg.Username, req.Remember)
	c.JSON(http.StatusOK, gin.H{"username": s.cfg.Username})
}

func (s *Server) handleLogout(c *gin.Context) {
	s.clearSession(c)
	c.Status(http.StatusNoContent)
}

func (s *Server) handleMe(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"username": c.GetString("user")})
}
