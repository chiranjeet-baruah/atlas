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

func batchStatusCases() []struct {
	name        string
	stub        *stubBatchStatusRunner
	wantStatus  int
	wantBodyHas string
} {
	return []struct {
		name        string
		stub        *stubBatchStatusRunner
		wantStatus  int
		wantBodyHas string
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
			name:        "domain.ErrNotFound renders not-found page",
			stub:        &stubBatchStatusRunner{err: fmt.Errorf("get batch batch-1: %w", domain.ErrNotFound)},
			wantStatus:  http.StatusNotFound,
			wantBodyHas: "Not found",
		},
		{
			name:        "any other error renders generic error page",
			stub:        &stubBatchStatusRunner{err: errors.New("db unreachable")},
			wantStatus:  http.StatusInternalServerError,
			wantBodyHas: "db unreachable",
		},
	}
}

func TestBatchPageHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range batchStatusCases() {
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
		})
	}
}

func TestBatchRowsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range batchStatusCases() {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.GET("/ui/batch/:batch_id/rows", web.NewBatchRowsHandler(tc.stub))

			req := httptest.NewRequest("GET", "/ui/batch/batch-1/rows", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("expected body to contain %q, got %s", tc.wantBodyHas, rec.Body.String())
			}
		})
	}
}
