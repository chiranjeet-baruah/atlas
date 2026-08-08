package web_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "resumesearch/internal/adapter/driver/web"
	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
)

type stubBatchStatusRunner struct {
	resp dto.BatchStatusResponse
	err  error
}

func (s *stubBatchStatusRunner) ByBatchID(ctx context.Context, batchID string) (dto.BatchStatusResponse, error) {
	return s.resp, s.err
}

func batchPageCases() []struct {
	name          string
	stub          *stubBatchStatusRunner
	wantStatus    int
	wantBodyHas   string
	wantBodyLacks string
} {
	return []struct {
		name          string
		stub          *stubBatchStatusRunner
		wantStatus    int
		wantBodyHas   string
		wantBodyLacks string
	}{
		{
			name: "found batch renders its resumes",
			stub: &stubBatchStatusRunner{resp: dto.BatchStatusResponse{
				BatchID: "batch-1",
				Resumes: []dto.StatusResponse{{ID: "r1", Filename: "a.pdf", Status: "DONE", Stage: "EMBED"}},
			}},
			wantStatus:  http.StatusOK,
			wantBodyHas: "a.pdf",
		},
		{
			name:        "domain.ErrNotFound renders the not-found page",
			stub:        &stubBatchStatusRunner{err: fmt.Errorf("get batch batch-1: %w", domain.ErrNotFound)},
			wantStatus:  http.StatusNotFound,
			wantBodyHas: "Not found",
		},
		{
			name:          "any other error renders the generic error page",
			stub:          &stubBatchStatusRunner{err: errors.New("db unreachable at 10.0.0.5:5432")},
			wantStatus:    http.StatusInternalServerError,
			wantBodyHas:   "Reference: internal-error",
			wantBodyLacks: "10.0.0.5",
		},
	}
}

func batchRowsCases() []struct {
	name           string
	stub           *stubBatchStatusRunner
	wantBodyHas    string
	wantBodyHasRef string
	wantBodyLacks  string
} {
	return []struct {
		name           string
		stub           *stubBatchStatusRunner
		wantBodyHas    string
		wantBodyHasRef string
		wantBodyLacks  string
	}{
		{
			name: "found batch renders its resumes",
			stub: &stubBatchStatusRunner{resp: dto.BatchStatusResponse{
				BatchID: "batch-1",
				Resumes: []dto.StatusResponse{{ID: "r1", Filename: "a.pdf", Status: "DONE", Stage: "EMBED"}},
			}},
			wantBodyHas: "a.pdf",
		},
		{
			name:        "domain.ErrNotFound renders inline in the fragment, not a 404 page",
			stub:        &stubBatchStatusRunner{err: fmt.Errorf("get batch batch-1: %w", domain.ErrNotFound)},
			wantBodyHas: "find what you were looking for",
		},
		{
			name:           "any other error renders inline in the fragment, not a 500 page",
			stub:           &stubBatchStatusRunner{err: errors.New("db unreachable at 10.0.0.5:5432")},
			wantBodyHas:    "Something went wrong",
			wantBodyHasRef: "(ref: internal-error)",
			wantBodyLacks:  "10.0.0.5",
		},
	}
}

func TestBatchPageHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range batchPageCases() {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.GET("/ui/batch/:batch_id", web.NewBatchPageHandler(tc.stub))

			req := httptest.NewRequest("GET", "/ui/batch/batch-1", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("expected body to contain %q, got %s", tc.wantBodyHas, rec.Body.String())
			}
			if tc.wantBodyLacks != "" && strings.Contains(rec.Body.String(), tc.wantBodyLacks) {
				t.Errorf("expected body not to contain %q, got %s", tc.wantBodyLacks, rec.Body.String())
			}
		})
	}
}

func TestBatchRowsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range batchRowsCases() {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.GET("/ui/batch/:batch_id/rows", web.NewBatchRowsHandler(tc.stub))

			req := httptest.NewRequest("GET", "/ui/batch/batch-1/rows", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 (fragment always renders inline, even on error), got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("expected body to contain %q, got %s", tc.wantBodyHas, rec.Body.String())
			}
			if tc.wantBodyHasRef != "" && !strings.Contains(rec.Body.String(), tc.wantBodyHasRef) {
				t.Errorf("expected body to contain slug reference %q, got %s", tc.wantBodyHasRef, rec.Body.String())
			}
			if tc.wantBodyLacks != "" && strings.Contains(rec.Body.String(), tc.wantBodyLacks) {
				t.Errorf("expected body not to contain %q, got %s", tc.wantBodyLacks, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `id="rows"`) {
				t.Errorf("expected the batch_rows fragment, not a full error page, got %s", rec.Body.String())
			}
		})
	}
}
