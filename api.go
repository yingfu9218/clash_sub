package main

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type subBody struct {
	Name    string `json:"name"`
	Note    string `json:"note"`
	Content string `json:"content"`
}

func (b *subBody) validate() (string, bool) {
	b.Name = strings.TrimSpace(b.Name)
	b.Note = strings.TrimSpace(b.Note)

	switch {
	case b.Name == "":
		return "订阅名称不能为空", false
	case len([]rune(b.Name)) > 60:
		return "订阅名称最多 60 个字", false
	case len([]rune(b.Note)) > 120:
		return "备注最多 120 个字", false
	case len(b.Content) > MaxContentBytes:
		return "配置太大了，单个订阅上限 2 MB", false
	}
	return "", true
}

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "订阅编号不对"})
		return 0, false
	}
	return id, true
}

// fail maps a store error onto a response. YAML errors carry the parser's own
// message through, because it names the line the operator has to fix.
func fail(c *gin.Context, err error) {
	if errors.Is(err, ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "配置不是合法的 YAML：" + err.Error()})
}

func (s *Server) listSubs(c *gin.Context) {
	c.JSON(http.StatusOK, s.store.List())
}

func (s *Server) getSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	it, err := s.store.Get(id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, it)
}

func (s *Server) createSub(c *gin.Context) {
	var b subBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}
	if msg, ok := b.validate(); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	it, err := s.store.Create(b.Name, b.Note, b.Content)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, it)
}

func (s *Server) updateSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var b subBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}
	if msg, ok := b.validate(); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	it, err := s.store.Update(id, b.Name, b.Note, b.Content)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, it)
}

func (s *Server) patchSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&b); err != nil || b.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不对"})
		return
	}
	it, err := s.store.SetEnabled(id, *b.Enabled)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, it)
}

func (s *Server) rotateToken(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	it, err := s.store.RotateToken(id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, it)
}

func (s *Server) deleteSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := s.store.Delete(id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// serveSubscription is the only unauthenticated route that reads data. The
// token is the credential, so a miss and a disabled subscription look the same.
func (s *Server) serveSubscription(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		c.String(http.StatusNotFound, "not found")
		return
	}
	it, err := s.store.ByToken(token)
	if err != nil {
		c.String(http.StatusNotFound, "not found")
		return
	}

	// Clash clients read the filename for the profile name, and the update
	// interval for how often to refetch.
	filename := url.PathEscape(it.Name + ".yaml")
	c.Header("Content-Disposition", "attachment; filename*=UTF-8''"+filename)
	c.Header("Profile-Update-Interval", "24")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/yaml; charset=utf-8", []byte(it.Content))
}
