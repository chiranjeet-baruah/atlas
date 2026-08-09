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

// TestSearchSubmitHandler_Rendering covers everything the "search_results"
// fragment can render: an inline validation error (no use-case call), a
// successful result set (in server order, with its view-resume link and
// match percentage), the empty-results state, and a use-case error (generic
// message only — the real error must never reach the response body).
func TestSearchSubmitHandler_Rendering(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name          string
		form          url.Values
		stub          *stubSearchRunner
		wantCalled    bool
		wantBodyHas   []string
		wantBodyLacks []string
	}{
		{
			name:        "empty query is rejected before the use case runs",
			form:        url.Values{"query": {""}},
			stub:        &stubSearchRunner{},
			wantCalled:  false,
			wantBodyHas: []string{"enter a search query"},
		},
		{
			name:        "non-numeric min_years is rejected before the use case runs",
			form:        url.Values{"query": {"go engineer"}, "min_years": {"abc"}},
			stub:        &stubSearchRunner{},
			wantCalled:  false,
			wantBodyHas: []string{"min years must be a number"},
		},
		{
			name: "results render in server order with a match percentage and a view-resume link per row",
			form: url.Values{"query": {"go engineer"}},
			stub: &stubSearchRunner{resp: dto.SearchResponse{Results: []dto.SearchResultDTO{
				{ID: "r1", Filename: "first.pdf", MatchPercentage: 42},
				{ID: "r2", Filename: "second.pdf", MatchPercentage: 91},
			}}},
			wantCalled: true,
			wantBodyHas: []string{
				"Match %", "42%", "91%",
				`href="/resumes/r1/file"`, `href="/resumes/r2/file"`, `target="_blank"`,
			},
		},
		{
			name:        "no matches renders the empty-state row with colspan matching the table's column count",
			form:        url.Values{"query": {"go engineer"}},
			stub:        &stubSearchRunner{resp: dto.SearchResponse{Results: nil}},
			wantCalled:  true,
			wantBodyHas: []string{"No matches.", `colspan="6"`},
		},
		{
			name:        "use-case error renders a generic message and slug, never the real error",
			form:        url.Values{"query": {"go engineer"}},
			stub:        &stubSearchRunner{err: errors.New("embedding service unreachable at 10.0.0.7:9000")},
			wantCalled:  true,
			wantBodyHas: []string{"Something went wrong", "(ref: internal-error)", `id="results"`},
			wantBodyLacks: []string{
				"10.0.0.7", "embedding service unreachable",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.POST("/ui/search", web.NewSearchSubmitHandler(tc.stub))

			req := newSearchFormRequest(tc.form)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200 (fragment renders inline, not a full error page), got %d: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range tc.wantBodyHas {
				if !strings.Contains(body, want) {
					t.Errorf("expected body to contain %q, got %s", want, body)
				}
			}
			for _, lack := range tc.wantBodyLacks {
				if strings.Contains(body, lack) {
					t.Errorf("expected body NOT to contain %q (internal detail leaked), got %s", lack, body)
				}
			}
			if tc.stub.called != tc.wantCalled {
				t.Errorf("use case called = %v, want %v", tc.stub.called, tc.wantCalled)
			}
		})
	}
}

// TestSearchSubmitHandler_Success_ResultOrderPreserved is a separate,
// narrower check for one property the table above can't express well: with
// several results, the response body must list them in exactly the order
// the use case returned them, since that order (best match first) is
// meaningful and must never be silently reshuffled by rendering.
func TestSearchSubmitHandler_Success_ResultOrderPreserved(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stub := &stubSearchRunner{resp: dto.SearchResponse{Results: []dto.SearchResultDTO{
		{ID: "r1", Filename: "first.pdf", MatchPercentage: 91},
		{ID: "r2", Filename: "second.pdf", MatchPercentage: 42},
	}}}
	router := gin.New()
	router.SetHTMLTemplate(web.ParseTemplates())
	router.POST("/ui/search", web.NewSearchSubmitHandler(stub))

	req := newSearchFormRequest(url.Values{"query": {"go engineer"}})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	body := rec.Body.String()
	firstIdx := strings.Index(body, "first.pdf")
	secondIdx := strings.Index(body, "second.pdf")
	if firstIdx == -1 || secondIdx == -1 || firstIdx > secondIdx {
		t.Errorf("expected results rendered in server order (first.pdf before second.pdf), got %s", body)
	}
}

// TestSearchSubmitHandler_FormParsing verifies the multi-value/optional form
// fields (required_skills, min_years) are parsed into the request handed to
// the use case, independent of how the response is rendered.
func TestSearchSubmitHandler_FormParsing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	three := 3.0
	cases := []struct {
		name         string
		form         url.Values
		wantSkills   []string
		wantMinYears *float64
		wantLocation string
	}{
		{
			name: "required_skills and min_years and location all parsed",
			form: url.Values{
				"query":           {"go engineer"},
				"required_skills": {"go, kafka"},
				"min_years":       {"3"},
				"location":        {"Remote"},
			},
			wantSkills:   []string{"go", "kafka"},
			wantMinYears: &three,
			wantLocation: "Remote",
		},
		{
			name:         "omitted min_years stays nil, never coerced to 0",
			form:         url.Values{"query": {"go engineer"}},
			wantSkills:   nil,
			wantMinYears: nil,
			wantLocation: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubSearchRunner{resp: dto.SearchResponse{}}
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.POST("/ui/search", web.NewSearchSubmitHandler(stub))

			req := newSearchFormRequest(tc.form)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if !stub.called {
				t.Fatalf("expected use case to be called")
			}
			if len(stub.gotReq.RequiredSkills) != len(tc.wantSkills) {
				t.Fatalf("RequiredSkills = %v, want %v", stub.gotReq.RequiredSkills, tc.wantSkills)
			}
			for i, want := range tc.wantSkills {
				if stub.gotReq.RequiredSkills[i] != want {
					t.Errorf("RequiredSkills[%d] = %q, want %q", i, stub.gotReq.RequiredSkills[i], want)
				}
			}
			switch {
			case tc.wantMinYears == nil && stub.gotReq.MinYears != nil:
				t.Errorf("MinYears = %v, want nil", *stub.gotReq.MinYears)
			case tc.wantMinYears != nil && (stub.gotReq.MinYears == nil || *stub.gotReq.MinYears != *tc.wantMinYears):
				t.Errorf("MinYears = %v, want %v", stub.gotReq.MinYears, *tc.wantMinYears)
			}
			if stub.gotReq.Location != tc.wantLocation {
				t.Errorf("Location = %q, want %q", stub.gotReq.Location, tc.wantLocation)
			}
		})
	}
}
