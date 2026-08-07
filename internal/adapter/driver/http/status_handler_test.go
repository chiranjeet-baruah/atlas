package http_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	httpdriver "resumesearch/internal/adapter/driver/http"
	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
)

type stubStatusRunner struct {
	byIDResp dto.StatusResponse
	err      error
}

func (s *stubStatusRunner) ByID(ctx context.Context, id string) (dto.StatusResponse, error) {
	return s.byIDResp, s.err
}

func TestStatusHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		stub        *stubStatusRunner
		wantStatus  int
		wantBodyHas string
	}{
		{
			name:        "found resume returns 200 with its status",
			stub:        &stubStatusRunner{byIDResp: dto.StatusResponse{ID: "r1", Filename: "a.pdf", Status: "DONE"}},
			wantStatus:  200,
			wantBodyHas: "DONE",
		},
		{
			name:        "domain.ErrNotFound maps to 404",
			stub:        &stubStatusRunner{err: fmt.Errorf("get resume r1: %w", domain.ErrNotFound)},
			wantStatus:  404,
			wantBodyHas: "not found",
		},
		{
			name:        "any other error maps to 500, not 404",
			stub:        &stubStatusRunner{err: fmt.Errorf("db unreachable")},
			wantStatus:  500,
			wantBodyHas: "db unreachable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/resumes/:id", httpdriver.NewStatusHandler(tc.stub))

			req := httptest.NewRequest("GET", "/resumes/r1", nil)
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

type stubBatchStatusRunner struct {
	resp dto.BatchStatusResponse
	err  error
}

func (s *stubBatchStatusRunner) ByBatchID(ctx context.Context, batchID string) (dto.BatchStatusResponse, error) {
	return s.resp, s.err
}

func TestBatchStatusHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		stub        *stubBatchStatusRunner
		wantStatus  int
		wantBodyHas string
	}{
		{
			name:        "found batch returns 200 with its resumes",
			stub:        &stubBatchStatusRunner{resp: dto.BatchStatusResponse{BatchID: "batch-1", Resumes: []dto.StatusResponse{{ID: "r1", Status: "DONE"}}}},
			wantStatus:  200,
			wantBodyHas: "batch-1",
		},
		{
			name:        "domain.ErrNotFound maps to 404",
			stub:        &stubBatchStatusRunner{err: fmt.Errorf("get batch batch-1: %w", domain.ErrNotFound)},
			wantStatus:  404,
			wantBodyHas: "not found",
		},
		{
			name:        "any other error maps to 500",
			stub:        &stubBatchStatusRunner{err: fmt.Errorf("db unreachable")},
			wantStatus:  500,
			wantBodyHas: "db unreachable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/resumes/batch/:batch_id", httpdriver.NewBatchStatusHandler(tc.stub))

			req := httptest.NewRequest("GET", "/resumes/batch/batch-1", nil)
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
