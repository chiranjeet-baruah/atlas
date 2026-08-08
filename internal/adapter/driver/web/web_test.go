package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "resumesearch/internal/adapter/driver/web"
	"resumesearch/internal/dto"
)

func TestNew_RegistersAllRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upload := &stubUploadRunner{resp: dto.UploadBatchResponse{BatchID: "batch-1"}}
	status := &stubBatchStatusRunner{resp: dto.BatchStatusResponse{BatchID: "batch-1"}}
	search := &stubSearchRunner{}

	router := gin.New()
	web.New(router, upload, status, search)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		contType   string
		wantStatus int
	}{
		{name: "GET upload form", method: "GET", path: "/ui/upload", wantStatus: http.StatusOK},
		{name: "GET search form", method: "GET", path: "/ui/search", wantStatus: http.StatusOK},
		{name: "GET batch page", method: "GET", path: "/ui/batch/batch-1", wantStatus: http.StatusOK},
		{name: "GET batch rows", method: "GET", path: "/ui/batch/batch-1/rows", wantStatus: http.StatusOK},
		{
			name: "POST upload with no files re-renders form", method: "POST", path: "/ui/upload",
			wantStatus: http.StatusOK,
		},
		{
			name: "POST search with empty query re-renders results with error", method: "POST", path: "/ui/search",
			body: url.Values{"query": {""}}.Encode(), contType: "application/x-www-form-urlencoded",
			wantStatus: http.StatusOK,
		},
		{name: "GET static style.css", method: "GET", path: "/ui/static/style.css", wantStatus: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", tc.contType)
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPageHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		path        string
		handler     gin.HandlerFunc
		wantBodyHas string
	}{
		{
			name:        "upload page renders the upload form",
			path:        "/ui/upload",
			handler:     web.NewUploadPageHandler(),
			wantBodyHas: "Upload resumes",
		},
		{
			name:        "search page renders the search form",
			path:        "/ui/search",
			handler:     web.NewSearchPageHandler(),
			wantBodyHas: "Search resumes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.GET(tc.path, tc.handler)

			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("expected body to contain %q, got %s", tc.wantBodyHas, rec.Body.String())
			}
		})
	}
}
