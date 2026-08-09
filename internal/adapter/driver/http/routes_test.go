package http_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	httpdriver "resumesearch/internal/adapter/driver/http"
	"resumesearch/internal/dto"
)

// TestResumeRoutes_DispatchToTheRightHandler registers every /resumes/...
// GET route on a single router, the way cmd/app/serve.go actually does,
// and checks each request lands on the handler it's meant to. This is the
// one thing go build/go vet cannot catch: /resumes/:id, /resumes/:id/file,
// and /resumes/batch/:batch_id mix a wildcard segment with a literal
// "batch" segment at the same position in the route tree — exactly the
// shape that can either panic at registration or silently misroute (e.g.
// GET /resumes/batch/b1 being swallowed by :id with id="batch" instead of
// reaching the batch handler).
func TestResumeRoutes_DispatchToTheRightHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pdfPath := writeTempFile(t, t.TempDir(), "0_a.pdf", "pdf bytes")

	router := gin.New()
	router.GET("/resumes/:id", httpdriver.NewStatusHandler(&stubStatusRunner{
		byIDResp: dto.StatusResponse{ID: "status-handler-reached"},
	}))
	router.GET("/resumes/:id/file", httpdriver.NewResumeFileHandler(&stubResumeFileRunner{
		resp: dto.ResumeFileInfo{Filename: "a.pdf", FilePath: pdfPath},
	}))
	router.GET("/resumes/batch/:batch_id", httpdriver.NewBatchStatusHandler(&stubBatchStatusRunner{
		resp: dto.BatchStatusResponse{BatchID: "batch-handler-reached"},
	}))

	cases := []struct {
		name        string
		path        string
		wantBodyHas string
	}{
		{name: "GET /resumes/:id hits the status handler", path: "/resumes/r1", wantBodyHas: "status-handler-reached"},
		{name: "GET /resumes/:id/file hits the file handler", path: "/resumes/r1/file", wantBodyHas: "pdf bytes"},
		{name: "GET /resumes/batch/:batch_id hits the batch handler, not :id with id=batch", path: "/resumes/batch/b1", wantBodyHas: "batch-handler-reached"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("GET %s: expected body to contain %q, got %q", tc.path, tc.wantBodyHas, rec.Body.String())
			}
		})
	}
}
