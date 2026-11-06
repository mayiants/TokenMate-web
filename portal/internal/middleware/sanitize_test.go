package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSanitizeInput_RemovesNullBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SanitizeInput())
	r.POST("/echo", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(body))
	})

	body := `{"a":"x` + "\x00" + `y"}`
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), "\x00") {
		t.Error("null byte was not removed")
	}
	if !strings.Contains(w.Body.String(), "xy") {
		t.Errorf("body mangled: %q", w.Body.String())
	}
}

func TestSanitizeInput_RejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SanitizeInput())
	r.POST("/echo", func(c *gin.Context) { c.Status(http.StatusOK) })

	// 10 MB + 1 byte
	body := strings.NewReader(strings.Repeat("x", (10<<20)+1))
	req := httptest.NewRequest(http.MethodPost, "/echo", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

func TestSanitizeInput_PassesNonJSONThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SanitizeInput())
	r.POST("/echo", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(body))
	})

	body := "a=1\x00&b=2"
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Body.String() != body {
		t.Errorf("non-JSON body must pass through untouched, got %q", w.Body.String())
	}
}
