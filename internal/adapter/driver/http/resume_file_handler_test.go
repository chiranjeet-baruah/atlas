package http_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	httpdriver "resumesearch/internal/adapter/driver/http"
	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
)

type stubResumeFileRunner struct {
	resp dto.ResumeFileInfo
	err  error
}

func (s *stubResumeFileRunner) FileByID(ctx context.Context, id string) (dto.ResumeFileInfo, error) {
	return s.resp, s.err
}

func TestResumeFileHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()

	pdfPath := writeTempFile(t, dir, "0_a.pdf", "%PDF-1.4 fake pdf bytes")
	docxPath := writeTempFile(t, dir, "0_a.docx", "fake docx bytes")
	noExtPath := writeTempFile(t, dir, "0_a", "fake bytes, no extension")

	cases := []struct {
		name               string
		stub               *stubResumeFileRunner
		wantStatus         int
		wantBodyHas        string
		wantBodyLacks      string // substring that must NOT appear in the body; "" means skip this check
		wantContentDisp    string // substring expected in Content-Disposition; "" means header must be absent
		wantNosniffPresent bool
	}{
		{
			name:               ".pdf is served inline with nosniff set",
			stub:               &stubResumeFileRunner{resp: dto.ResumeFileInfo{Filename: "a.pdf", FilePath: pdfPath}},
			wantStatus:         200,
			wantBodyHas:        "fake pdf bytes",
			wantContentDisp:    "",
			wantNosniffPresent: true,
		},
		{
			name:            ".docx is forced to download as an attachment",
			stub:            &stubResumeFileRunner{resp: dto.ResumeFileInfo{Filename: "a.docx", FilePath: docxPath}},
			wantStatus:      200,
			wantBodyHas:     "fake docx bytes",
			wantContentDisp: "attachment",
		},
		{
			name:               "extension match is case-insensitive",
			stub:               &stubResumeFileRunner{resp: dto.ResumeFileInfo{Filename: "A.PDF", FilePath: pdfPath}},
			wantStatus:         200,
			wantBodyHas:        "fake pdf bytes",
			wantContentDisp:    "",
			wantNosniffPresent: true,
		},
		{
			name:            "no extension is forced to download, not served inline",
			stub:            &stubResumeFileRunner{resp: dto.ResumeFileInfo{Filename: "a", FilePath: noExtPath}},
			wantStatus:      200,
			wantBodyHas:     "fake bytes, no extension",
			wantContentDisp: "attachment",
		},
		{
			name:        "domain.ErrNotFound maps to 404",
			stub:        &stubResumeFileRunner{err: fmt.Errorf("get resume r1: %w", domain.ErrNotFound)},
			wantStatus:  404,
			wantBodyHas: "not found",
		},
		{
			// Unlike status_handler.go's writeResumeError (which does put
			// err.Error() into a 500 body for a trusted JSON client), this
			// handler is linked directly from the search page as a browser
			// navigation target — the real error must never end up in a
			// browser tab.
			name:          "any other error maps to 500 with a generic message, never the real error",
			stub:          &stubResumeFileRunner{err: fmt.Errorf("db unreachable at 10.0.0.7:5432")},
			wantStatus:    500,
			wantBodyHas:   "something went wrong",
			wantBodyLacks: "10.0.0.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/resumes/:id/file", httpdriver.NewResumeFileHandler(tc.stub))

			req := httptest.NewRequest("GET", "/resumes/r1/file", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("expected body to contain %q, got %q", tc.wantBodyHas, rec.Body.String())
			}
			if tc.wantBodyLacks != "" && strings.Contains(rec.Body.String(), tc.wantBodyLacks) {
				t.Errorf("expected body NOT to contain %q (internal detail leaked), got %q", tc.wantBodyLacks, rec.Body.String())
			}

			disp := rec.Header().Get("Content-Disposition")
			if tc.wantContentDisp == "" {
				if disp != "" {
					t.Errorf("expected no Content-Disposition header, got %q", disp)
				}
			} else if !strings.Contains(disp, tc.wantContentDisp) {
				t.Errorf("expected Content-Disposition to contain %q, got %q", tc.wantContentDisp, disp)
			}

			nosniff := rec.Header().Get("X-Content-Type-Options")
			if tc.wantNosniffPresent && nosniff != "nosniff" {
				t.Errorf("expected X-Content-Type-Options: nosniff, got %q", nosniff)
			}
		})
	}
}

// writeTempFile writes content under dir/name and returns its full path,
// failing the test on any error.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file %s: %v", path, err)
	}
	return path
}
