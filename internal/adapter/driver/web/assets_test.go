package web_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "resumesearch/internal/adapter/driver/web"
)

func TestParseTemplates_RendersErrorPages(t *testing.T) {
	tmpl := web.ParseTemplates()

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "error_page", map[string]any{"Error": "boom"}); err != nil {
		t.Fatalf("execute error_page: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected rendered error_page to contain %q, got %s", "boom", buf.String())
	}

	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "not_found_page", map[string]any{}); err != nil {
		t.Fatalf("execute not_found_page: %v", err)
	}
	if !strings.Contains(buf.String(), "Not found") {
		t.Errorf("expected rendered not_found_page to contain %q, got %s", "Not found", buf.String())
	}
}

func TestStaticFS_ServesStyleAndHtmx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.StaticFS("/ui/static", web.StaticFS())

	req := httptest.NewRequest("GET", "/ui/static/style.css", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("style.css: expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "body {") {
		t.Errorf("style.css: expected body to contain %q, got %s", "body {", rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/ui/static/htmx.min.js", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("htmx.min.js: expected 200, got %d", rec.Code)
	}
	if rec.Body.Len() < 1000 {
		t.Errorf("htmx.min.js: expected a real vendored file (>1000 bytes), got %d bytes", rec.Body.Len())
	}
}
