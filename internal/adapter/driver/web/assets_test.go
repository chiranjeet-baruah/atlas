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

	tests := []struct {
		templateName  string
		data          any
		wantSubstring string
	}{
		{
			templateName:  "error_page",
			data:          map[string]any{"Message": "boom", "Slug": "test-slug"},
			wantSubstring: "boom",
		},
		{
			templateName:  "not_found_page",
			data:          map[string]any{},
			wantSubstring: "Not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.templateName, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, tc.templateName, tc.data); err != nil {
				t.Fatalf("execute %s: %v", tc.templateName, err)
			}
			if !strings.Contains(buf.String(), tc.wantSubstring) {
				t.Errorf("expected rendered %s to contain %q, got %s", tc.templateName, tc.wantSubstring, buf.String())
			}
		})
	}
}

func TestStaticFS_ServesStyleAndHtmx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.StaticFS("/ui/static", web.StaticFS())

	tests := []struct {
		name  string
		path  string
		check func(t *testing.T, wantStatus int, body string)
	}{
		{
			name: "style.css",
			path: "/ui/static/style.css",
			check: func(t *testing.T, wantStatus int, body string) {
				if !strings.Contains(body, "body {") {
					t.Errorf("style.css: expected body to contain %q, got %s", "body {", body)
				}
			},
		},
		{
			name: "htmx.min.js",
			path: "/ui/static/htmx.min.js",
			check: func(t *testing.T, wantStatus int, body string) {
				if len(body) < 1000 {
					t.Errorf("htmx.min.js: expected a real vendored file (>1000 bytes), got %d bytes", len(body))
				}
				if !strings.Contains(body, "htmx") {
					t.Errorf("htmx.min.js: expected body to contain %q, got first 200 chars: %s", "htmx", body[:min(200, len(body))])
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d", tc.name, rec.Code)
			}
			tc.check(t, rec.Code, rec.Body.String())
		})
	}
}
