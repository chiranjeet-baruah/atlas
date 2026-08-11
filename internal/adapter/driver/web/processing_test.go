package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "resumesearch/internal/adapter/driver/web"
	"resumesearch/internal/dto"
)

// validBatchID is a syntactically valid UUID used wherever a test needs a
// batch_id that passes NewBatchLookupHandler's uuid.Parse check.
const validBatchID = "11111111-1111-1111-1111-111111111111"

type stubBatchListRunner struct {
	summaries []dto.BatchSummary
	err       error
}

func (s *stubBatchListRunner) ListBatches(ctx context.Context) ([]dto.BatchSummary, error) {
	return s.summaries, s.err
}

func TestProcessingPageHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name          string
		stub          *stubBatchListRunner
		wantStatus    int
		wantBodyHas   string
		wantBodyLacks string
	}{
		{
			name:        "batches render in the table",
			stub:        &stubBatchListRunner{summaries: []dto.BatchSummary{{BatchID: validBatchID, Total: 3, Done: 3}}},
			wantStatus:  http.StatusOK,
			wantBodyHas: validBatchID,
		},
		{
			name:        "no batches yet renders the empty-state row",
			stub:        &stubBatchListRunner{summaries: []dto.BatchSummary{}},
			wantStatus:  http.StatusOK,
			wantBodyHas: "No batches yet.",
		},
		{
			name:          "use-case error renders the generic error page, not the raw error",
			stub:          &stubBatchListRunner{err: errors.New("db unreachable at 10.0.0.5:5432")},
			wantStatus:    http.StatusInternalServerError,
			wantBodyHas:   "Reference: internal-error",
			wantBodyLacks: "10.0.0.5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.GET("/ui/processing", web.NewProcessingPageHandler(tc.stub))

			req := httptest.NewRequest("GET", "/ui/processing", nil)
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

func TestProcessingRowsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name           string
		stub           *stubBatchListRunner
		wantBodyHas    string
		wantBodyHasRef string
		wantBodyLacks  string
	}{
		{
			name:        "batches render in the fragment",
			stub:        &stubBatchListRunner{summaries: []dto.BatchSummary{{BatchID: validBatchID, Total: 3, Done: 3}}},
			wantBodyHas: validBatchID,
		},
		{
			// A use-case error must render inline inside the fragment (not a
			// 500 page) with both the generic message AND the error-slug
			// reference surviving into the HTML — htmx won't swap a non-2xx
			// response into its target, so a full-page error response here
			// would leave the fragment's target empty.
			name:           "use-case error renders inline with its slug, not a 500 page",
			stub:           &stubBatchListRunner{err: errors.New("db unreachable at 10.0.0.5:5432")},
			wantBodyHas:    "Something went wrong",
			wantBodyHasRef: "(ref: internal-error)",
			wantBodyLacks:  "10.0.0.5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.GET("/ui/processing/rows", web.NewProcessingRowsHandler(tc.stub))

			req := httptest.NewRequest("GET", "/ui/processing/rows", nil)
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
			if !strings.Contains(rec.Body.String(), `id="batches"`) {
				t.Errorf("expected the processing_rows fragment, not a full error page, got %s", rec.Body.String())
			}
		})
	}
}

func TestProcessingRowsHandler_TruncationNotice(t *testing.T) {
	gin.SetMode(gin.TestMode)

	summaries := make([]dto.BatchSummary, 0, 100)
	for i := 0; i < 100; i++ {
		summaries = append(summaries, dto.BatchSummary{BatchID: validBatchID})
	}

	router := gin.New()
	router.SetHTMLTemplate(web.ParseTemplates())
	router.GET("/ui/processing/rows", web.NewProcessingRowsHandler(&stubBatchListRunner{summaries: summaries}))

	req := httptest.NewRequest("GET", "/ui/processing/rows", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "Showing newest 100 batches.") {
		t.Errorf("expected truncation notice naming the cap, got %s", rec.Body.String())
	}
}

func TestBatchLookupHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name         string
		batchIDParam string
		stub         *stubBatchListRunner
		wantStatus   int
		wantLocation string
		wantBodyHas  string
	}{
		{
			name:         "valid UUID redirects to the batch page",
			batchIDParam: validBatchID,
			stub:         &stubBatchListRunner{},
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/ui/batch/" + validBatchID,
		},
		{
			name:         "empty batch_id re-renders the processing page with an inline error",
			batchIDParam: "",
			stub:         &stubBatchListRunner{summaries: []dto.BatchSummary{{BatchID: validBatchID}}},
			wantStatus:   http.StatusOK,
			wantBodyHas:  "Please enter a batch ID.",
		},
		{
			name:         "non-UUID batch_id re-renders the processing page with an inline error, not a 500",
			batchIDParam: "not-a-uuid",
			stub:         &stubBatchListRunner{summaries: []dto.BatchSummary{{BatchID: validBatchID}}},
			wantStatus:   http.StatusOK,
			wantBodyHas:  "not a valid batch ID",
		},
		{
			name:         "re-fetch failure on the error-recovery path renders the generic error page",
			batchIDParam: "not-a-uuid",
			stub:         &stubBatchListRunner{err: errors.New("db unreachable at 10.0.0.5:5432")},
			wantStatus:   http.StatusInternalServerError,
			wantBodyHas:  "Reference: internal-error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.GET("/ui/processing/lookup", web.NewBatchLookupHandler(tc.stub))

			req := httptest.NewRequest("GET", "/ui/processing/lookup?batch_id="+tc.batchIDParam, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantLocation != "" && rec.Header().Get("Location") != tc.wantLocation {
				t.Errorf("expected Location %q, got %q", tc.wantLocation, rec.Header().Get("Location"))
			}
			if tc.wantBodyHas != "" && !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("expected body to contain %q, got %s", tc.wantBodyHas, rec.Body.String())
			}
		})
	}
}
