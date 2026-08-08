package web

import (
	"context"
	"errors"
	"fmt"
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
		name          string
		err           error
		wantStatus    int
		wantSubstring string
	}{
		{
			name:          "domain.ErrNotFound maps to 404 with not_found_page",
			err:           domain.ErrNotFound,
			wantStatus:    http.StatusNotFound,
			wantSubstring: "Not found",
		},
		{
			name:          "generic error maps to 500 with error_page",
			err:           errors.New("something bad happened involving host 10.0.0.9"),
			wantStatus:    http.StatusInternalServerError,
			wantSubstring: "Reference: internal-error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			engine.SetHTMLTemplate(ParseTemplates())

			testErr := tc.err
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
			if strings.Contains(w.Body.String(), "10.0.0.9") {
				t.Errorf("internal error detail leaked into response body: %s", w.Body.String())
			}
		})
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantSlug    string
		wantMessage string
	}{
		{
			name:        "domain.ErrNotFound maps to not-found",
			err:         fmt.Errorf("get batch batch-1: %w", domain.ErrNotFound),
			wantStatus:  http.StatusNotFound,
			wantSlug:    "not-found",
			wantMessage: "We couldn't find what you were looking for.",
		},
		{
			name:        "any other error maps to internal-error",
			err:         errors.New("db unreachable at 10.0.0.5:5432"),
			wantStatus:  http.StatusInternalServerError,
			wantSlug:    "internal-error",
			wantMessage: "Something went wrong. Please try again.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, slug, message := classifyError(context.Background(), tc.err)
			if status != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, status)
			}
			if slug != tc.wantSlug {
				t.Errorf("expected slug %q, got %q", tc.wantSlug, slug)
			}
			if message != tc.wantMessage {
				t.Errorf("expected message %q, got %q", tc.wantMessage, message)
			}
		})
	}
}
