package http_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	httpdriver "resumesearch/internal/adapter/driver/http"
	"resumesearch/internal/constants"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)

type stubUploadRunner struct {
	gotFiles []service.UploadFile
	resp     dto.UploadBatchResponse
	err      error
}

func (s *stubUploadRunner) Run(ctx context.Context, files []service.UploadFile) (dto.UploadBatchResponse, error) {
	s.gotFiles = files
	return s.resp, s.err
}

func multipartBodyWithFiles(t *testing.T, filenames ...string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, name := range filenames {
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write([]byte("pdf-content")); err != nil {
			t.Fatalf("write form file content: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, mw.FormDataContentType()
}

func TestUploadHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name         string
		filenames    []string // nil means "send no multipart body at all"
		stub         *stubUploadRunner
		wantStatus   int
		wantBodyHas  string
		wantFileArgs int
	}{
		{
			name:         "single file is parsed and passed through, response echoes batch id",
			filenames:    []string{"a.pdf"},
			stub:         &stubUploadRunner{resp: dto.UploadBatchResponse{BatchID: "batch-1", Resumes: []dto.ResumeRef{{ID: "r1", Filename: "a.pdf"}}}},
			wantStatus:   200,
			wantBodyHas:  "batch-1",
			wantFileArgs: 1,
		},
		{
			name:         "multiple files under the same field are all parsed",
			filenames:    []string{"a.pdf", "b.pdf"},
			stub:         &stubUploadRunner{resp: dto.UploadBatchResponse{BatchID: "batch-2"}},
			wantStatus:   200,
			wantFileArgs: 2,
		},
		{
			name:       "no files under 'files' field is a 400, not a panic",
			filenames:  []string{},
			stub:       &stubUploadRunner{},
			wantStatus: 400,
		},
		{
			name:       "use case failure surfaces as 500",
			filenames:  []string{"a.pdf"},
			stub:       &stubUploadRunner{err: errors.New("db unreachable")},
			wantStatus: 500,
		},
		{
			name:      "partial failure still surfaces the batch id and resumes already committed, not just the error",
			filenames: []string{"a.pdf"},
			stub: &stubUploadRunner{
				resp: dto.UploadBatchResponse{BatchID: "partial-batch", Resumes: []dto.ResumeRef{{ID: "r1", Filename: "a.pdf"}}},
				err:  errors.New("kafka unreachable"),
			},
			wantStatus:  500,
			wantBodyHas: "partial-batch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/resumes/batch", httpdriver.NewUploadHandler(tc.stub))

			body, contentType := multipartBodyWithFiles(t, tc.filenames...)
			req := httptest.NewRequest("POST", "/resumes/batch", body)
			req.Header.Set("Content-Type", contentType)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantBodyHas != "" && !bytes.Contains(rec.Body.Bytes(), []byte(tc.wantBodyHas)) {
				t.Errorf("expected response body to contain %q, got %s", tc.wantBodyHas, rec.Body.String())
			}
			if tc.wantFileArgs > 0 && len(tc.stub.gotFiles) != tc.wantFileArgs {
				t.Errorf("expected handler to pass %d parsed files through, got %d", tc.wantFileArgs, len(tc.stub.gotFiles))
			}
		})
	}
}

// TestUploadHandler_RejectsOversizedBody proves the fix for the unbounded
// request-body finding: a request larger than constants.MaxUploadBytes
// must be rejected with 413 before it's fully read into memory, not
// accepted and processed.
func TestUploadHandler_RejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("files", "huge.pdf")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(make([]byte, constants.MaxUploadBytes+1024)); err != nil {
		t.Fatalf("write oversized content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	router := gin.New()
	router.POST("/resumes/batch", httpdriver.NewUploadHandler(&stubUploadRunner{}))

	req := httptest.NewRequest("POST", "/resumes/batch", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected %d, got %d: %s", http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	}
}

// TestUploadHandler_RejectsTooManyFiles proves a batch exceeding
// constants.MaxUploadFiles is rejected before any file is parsed or
// passed to the use case.
func TestUploadHandler_RejectsTooManyFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	filenames := make([]string, constants.MaxUploadFiles+1)
	for i := range filenames {
		filenames[i] = fmt.Sprintf("resume-%d.pdf", i)
	}

	stub := &stubUploadRunner{}
	router := gin.New()
	router.POST("/resumes/batch", httpdriver.NewUploadHandler(stub))

	body, contentType := multipartBodyWithFiles(t, filenames...)
	req := httptest.NewRequest("POST", "/resumes/batch", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
	if stub.gotFiles != nil {
		t.Errorf("expected the use case to never be invoked for an over-limit file count, got %d files", len(stub.gotFiles))
	}
}
