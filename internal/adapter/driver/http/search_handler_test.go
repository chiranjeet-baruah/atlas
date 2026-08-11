package http_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	httpdriver "resumesearch/internal/adapter/driver/http"
	"resumesearch/internal/constants"
	"resumesearch/internal/dto"
)

type stubSearchRunner struct {
	gotReq dto.SearchRequest
	resp   dto.SearchResponse
	err    error
}

func (s *stubSearchRunner) Run(ctx context.Context, req dto.SearchRequest) (dto.SearchResponse, error) {
	s.gotReq = req
	return s.resp, s.err
}

func TestSearchHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name           string
		body           string
		stub           *stubSearchRunner
		wantStatus     int
		wantQuery      string
		wantSkillCount int
	}{
		{
			name:           "valid request is parsed and forwarded",
			body:           `{"query":"backend engineer","required_skills":["go","postgres"]}`,
			stub:           &stubSearchRunner{resp: dto.SearchResponse{Results: []dto.SearchResultDTO{{ID: "r1", Filename: "a.pdf"}}}},
			wantStatus:     200,
			wantQuery:      "backend engineer",
			wantSkillCount: 2,
		},
		{
			name:       "missing required 'query' field is a 400",
			body:       `{"required_skills":["go"]}`,
			stub:       &stubSearchRunner{},
			wantStatus: 400,
		},
		{
			name:       "malformed JSON body is a 400, not a panic",
			body:       `{"query": `,
			stub:       &stubSearchRunner{},
			wantStatus: 400,
		},
		{
			name:       "use case failure surfaces as 500",
			body:       `{"query":"backend engineer"}`,
			stub:       &stubSearchRunner{err: errors.New("embedding service down")},
			wantStatus: 500,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/search", httpdriver.NewSearchHandler(tc.stub))

			req := httptest.NewRequest("POST", "/search", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantQuery != "" && tc.stub.gotReq.Query != tc.wantQuery {
				t.Errorf("got query %q, want %q", tc.stub.gotReq.Query, tc.wantQuery)
			}
			if tc.wantSkillCount > 0 && len(tc.stub.gotReq.RequiredSkills) != tc.wantSkillCount {
				t.Errorf("got %d required skills, want %d", len(tc.stub.gotReq.RequiredSkills), tc.wantSkillCount)
			}
		})
	}
}

// TestSearchHandler_RejectsOversizedBody asserts that a /search body larger
// than constants.MaxSearchBodyBytes is rejected with 413 before it's fully
// read into memory and bound.
func TestSearchHandler_RejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oversized := `{"query":"` + strings.Repeat("x", constants.MaxSearchBodyBytes+1024) + `"}`

	router := gin.New()
	router.POST("/search", httpdriver.NewSearchHandler(&stubSearchRunner{}))

	req := httptest.NewRequest("POST", "/search", bytes.NewBufferString(oversized))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected %d, got %d: %s", http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	}
}
