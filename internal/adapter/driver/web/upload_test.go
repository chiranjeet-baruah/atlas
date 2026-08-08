package web_test

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "resumesearch/internal/adapter/driver/web"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)

type stubUploadRunner struct {
	resp   dto.UploadBatchResponse
	err    error
	called bool
}

func (s *stubUploadRunner) Run(ctx context.Context, files []service.UploadFile) (dto.UploadBatchResponse, error) {
	s.called = true
	return s.resp, s.err
}

func newMultipartUploadRequest(t *testing.T, files map[string][]byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for name, content := range files {
		fw, err := w.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest("POST", "/ui/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestUploadSubmitHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name          string
		files         map[string][]byte
		stub          *stubUploadRunner
		wantStatus    int
		wantLocation  string
		wantBodyHas   string
		wantRunCalled bool
	}{
		{
			name:          "successful upload redirects to batch page",
			files:         map[string][]byte{"a.pdf": []byte("%PDF-1.4 fake")},
			stub:          &stubUploadRunner{resp: dto.UploadBatchResponse{BatchID: "batch-1"}},
			wantStatus:    http.StatusSeeOther,
			wantLocation:  "/ui/batch/batch-1",
			wantRunCalled: true,
		},
		{
			name:          "zero files re-renders form with inline error, use case not called",
			files:         map[string][]byte{},
			stub:          &stubUploadRunner{},
			wantStatus:    http.StatusOK,
			wantBodyHas:   "no files provided",
			wantRunCalled: false,
		},
		{
			name:          "use case error renders generic error page",
			files:         map[string][]byte{"a.pdf": []byte("%PDF-1.4 fake")},
			stub:          &stubUploadRunner{err: errors.New("kafka unreachable")},
			wantStatus:    http.StatusInternalServerError,
			wantBodyHas:   "kafka unreachable",
			wantRunCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.SetHTMLTemplate(web.ParseTemplates())
			router.POST("/ui/upload", web.NewUploadSubmitHandler(tc.stub))

			req := newMultipartUploadRequest(t, tc.files)
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
			if tc.stub.called != tc.wantRunCalled {
				t.Errorf("expected Run called=%v, got %v", tc.wantRunCalled, tc.stub.called)
			}
		})
	}
}
