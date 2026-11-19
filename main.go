package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

// The whole frontend ships inside the binary. `all:` keeps files that Go would
// otherwise skip, and means there is nothing to deploy alongside the executable.
//
//go:embed all:web
var webFS embed.FS

type Server struct {
	cfg      *Config
	store    *Store
	secret   []byte
	throttle *throttle
	assets   fs.FS
}

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	listen := flag.String("listen", "", "监听地址，覆盖配置文件（例如 :8088）")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	store, err := OpenStore(cfg.DataFile)
	if err != nil {
		log.Fatalf("打开数据文件失败: %v", err)
	}

	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("读取内嵌前端资源失败: %v", err)
	}

	srv := &Server{
		cfg:      cfg,
		store:    store,
		secret:   []byte(cfg.Secret),
		throttle: newThrottle(),
		assets:   assets,
	}

	if os.Getenv("CLASH_SUB_DEBUG") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	handler, err := srv.routes()
	if err != nil {
		log.Fatalf("设置可信代理失败: %v", err)
	}
	if len(cfg.loginAllow) == 0 {
		fmt.Println("注意：login_allow_ips 为空，任何 IP 都可以尝试登录")
	}

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		fmt.Printf("clash-sub 已启动  http://localhost%s\n", normalizeAddr(cfg.Listen))
		fmt.Printf("订阅地址格式      http://<你的地址>%s/subscribe?token=...\n\n", normalizeAddr(cfg.Listen))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("监听失败: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("关闭时出错: %v", err)
	}
	fmt.Println("已停止")
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return addr
}

func (s *Server) routes() (http.Handler, error) {
	r := gin.New()
	// gin trusts every proxy by default, which lets any client pick its own
	// address via X-Forwarded-For and walk past the login allowlist. An empty
	// list means "trust none": the TCP peer address is used.
	if err := r.SetTrustedProxies(s.cfg.TrustedProxies); err != nil {
		return nil, err
	}
	r.Use(gin.Logger(), gin.Recovery())

	// The subscription endpoint authenticates with its token, not a session.
	r.GET("/subscribe", s.serveSubscription)
	r.HEAD("/subscribe", s.serveSubscription)

	api := r.Group("/api")
	api.POST("/login", s.handleLogin)
	api.POST("/logout", s.handleLogout)

	auth := api.Group("", s.requireAuth())
	auth.GET("/me", s.handleMe)
	auth.GET("/subscriptions", s.listSubs)
	auth.POST("/subscriptions", s.createSub)
	auth.GET("/subscriptions/:id", s.getSub)
	auth.PUT("/subscriptions/:id", s.updateSub)
	auth.PATCH("/subscriptions/:id", s.patchSub)
	auth.DELETE("/subscriptions/:id", s.deleteSub)
	auth.POST("/subscriptions/:id/token", s.rotateToken)

	r.NoRoute(s.serveStatic)
	return r, nil
}

var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".ico":   "image/x-icon",
	".woff2": "font/woff2",
}

// serveStatic hands out the embedded frontend. Unknown /api paths get JSON so a
// typo in a fetch never returns a page of HTML to a caller expecting data.
//
// The bytes are written directly rather than through http.FileServer, because
// FileServer redirects "/index.html" to "./" — which, paired with mapping "/"
// onto index.html, is an infinite redirect loop.
func (s *Server) serveStatic(c *gin.Context) {
	p := c.Request.URL.Path
	if strings.HasPrefix(p, "/api/") {
		c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
		return
	}

	name := strings.TrimPrefix(p, "/")
	if name == "" {
		name = "index.html"
	}

	// Anything with no match is a 404, not a silent index.html. Directories are
	// refused too, so a request for /assets never renders a file listing.
	info, err := fs.Stat(s.assets, name)
	if err != nil || info.IsDir() {
		c.String(http.StatusNotFound, "not found")
		return
	}
	body, err := fs.ReadFile(s.assets, name)
	if err != nil {
		c.String(http.StatusNotFound, "not found")
		return
	}

	if strings.HasPrefix(name, "assets/vendor/") {
		c.Header("Cache-Control", "public, max-age=604800, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}

	ct := contentTypes[strings.ToLower(path.Ext(name))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Data(http.StatusOK, ct, body)
}
