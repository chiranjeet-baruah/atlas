package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "resumesearch/internal/adapter/driver/web"
	"resumesearch/internal/dto"
)

type stubSearchRunner struct {
	resp   dto.SearchResponse
	err    error
	gotReq dto.SearchRequest
	called bool
}

func (s *stubSearchRunner) Run(ctx context.Context, req dto.SearchRequest) (dto.SearchResponse, error) {
	s.called = true
	s.gotReq = req
	return s.resp, s.err
}

func newSearchFormRequest(form url.Values) *http.Request {
	req := httptest.NewRequest("POST", "/ui/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestSearchSubmitHandler_ValidationErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		form        url.Values
		wantBodyHas string
	}{
		{
			name:        "empty query",
			form:        url.Values{"query": {""}},
			wantBodyHas: "enter a search query",
		},
		{
			name:        "non-numeric min_years",
			form:        url.Values{"query": {"go engineer"}, "min_years": {"abc"}},
			wantBodyHas: "min years must be a number",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubSearchRunner{}
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.POST("/ui/search", web.NewSearchSubmitHandler(stub))

			req := newSearchFormRequest(tc.form)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("expected body to contain %q, got %s", tc.wantBodyHas, rec.Body.String())
			}
			if stub.called {
				t.Errorf("expected use case not to be called on validation failure")
			}
		})
	}
}

func TestSearchSubmitHandler_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := &stubSearchRunner{resp: dto.SearchResponse{Results: []dto.SearchResultDTO{
		{ID: "r1", Filename: "first.pdf", Distance: 0.9},
		{ID: "r2", Filename: "second.pdf", Distance: 0.1},
	}}}
	router := gin.New()
	router.SetHTMLTemplate(web.ParseTemplates())
	router.POST("/ui/search", web.NewSearchSubmitHandler(stub))

	req := newSearchFormRequest(url.Values{
		"query":           {"go engineer"},
		"required_skills": {"go, kafka"},
		"min_years":       {"3"},
		"location":        {"Remote"},
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "distance (lower = closer)") {
		t.Errorf("expected distance column label, got %s", body)
	}
	if strings.Contains(strings.ToLower(body), "score") {
		t.Errorf("must never label the distance column \"score\", got %s", body)
	}
	firstIdx := strings.Index(body, "first.pdf")
	secondIdx := strings.Index(body, "second.pdf")
	if firstIdx == -1 || secondIdx == -1 || firstIdx > secondIdx {
		t.Errorf("expected results rendered in server order (first.pdf before second.pdf), got %s", body)
	}

	if !stub.called {
		t.Fatalf("expected use case to be called")
	}
	if len(stub.gotReq.RequiredSkills) != 2 || stub.gotReq.RequiredSkills[0] != "go" || stub.gotReq.RequiredSkills[1] != "kafka" {
		t.Errorf("expected required_skills [go kafka], got %v", stub.gotReq.RequiredSkills)
	}
	if stub.gotReq.MinYears == nil || *stub.gotReq.MinYears != 3 {
		t.Errorf("expected min_years 3, got %v", stub.gotReq.MinYears)
	}
}

func TestSearchSubmitHandler_UseCaseError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := &stubSearchRunner{err: errors.New("embedding service unreachable")}
	router := gin.New()
	router.SetHTMLTemplate(web.ParseTemplates())
	router.POST("/ui/search", web.NewSearchSubmitHandler(stub))

	req := newSearchFormRequest(url.Values{"query": {"go engineer"}})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (fragment renders the error inline, not a full error page), got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "embedding service unreachable") {
		t.Errorf("expected error message in body, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `id="results"`) {
		t.Errorf("expected the search_results fragment, not a full error page, got %s", rec.Body.String())
	}
}
