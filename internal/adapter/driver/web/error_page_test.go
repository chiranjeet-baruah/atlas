package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/domain"
)

func TestRenderError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantSubstring  string
	}{
		{
			name:          "domain.ErrNotFound maps to 404 with not_found_page",
			err:           domain.ErrNotFound,
			wantStatus:    http.StatusNotFound,
			wantSubstring: "Not found",
		},
		{
			name:          "generic error maps to 500 with error_page",
			err:           errors.New("something bad happened"),
			wantStatus:    http.StatusInternalServerError,
			wantSubstring: "something bad happened",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a fresh engine for each test case
			engine := gin.New()
			engine.SetHTMLTemplate(ParseTemplates())

			// Set up a route that calls renderError with the test error
			testErr := tc.err // Capture in closure
			engine.GET("/test", func(c *gin.Context) {
				renderError(c, testErr)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			engine.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
			if !strings.Contains(w.Body.String(), tc.wantSubstring) {
				t.Errorf("expected body to contain %q, got %s", tc.wantSubstring, w.Body.String())
			}
		})
	}
}
