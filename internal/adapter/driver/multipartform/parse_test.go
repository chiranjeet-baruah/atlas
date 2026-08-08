package multipartform_test

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/adapter/driver/multipartform"
	"resumesearch/internal/constants"
)

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

func TestParseUploadFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tooManyFilenames := make([]string, constants.MaxUploadFiles+1)
	for i := range tooManyFilenames {
		tooManyFilenames[i] = fmt.Sprintf("resume-%d.pdf", i)
	}

	cases := []struct {
		name          string
		filenames     []string
		oversizedFile bool
		wantErr       bool
		wantTooLarge  bool
		wantFileCount int
	}{
		{name: "single file parses", filenames: []string{"a.pdf"}, wantFileCount: 1},
		{name: "multiple files under the same field all parse", filenames: []string{"a.pdf", "b.pdf"}, wantFileCount: 2},
		{name: "zero files is an InputError, not TooLarge", filenames: []string{}, wantErr: true, wantTooLarge: false},
		{name: "over the file-count limit is an InputError, not TooLarge", filenames: tooManyFilenames, wantErr: true, wantTooLarge: false},
		{name: "over the body-size limit is an InputError with TooLarge set", filenames: []string{"huge.pdf"}, oversizedFile: true, wantErr: true, wantTooLarge: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body *bytes.Buffer
			var contentType string
			if tc.oversizedFile {
				var buf bytes.Buffer
				mw := multipart.NewWriter(&buf)
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
				body, contentType = &buf, mw.FormDataContentType()
			} else {
				body, contentType = multipartBodyWithFiles(t, tc.filenames...)
			}

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/upload", body)
			req.Header.Set("Content-Type", contentType)
			c, _ := gin.CreateTestContext(w)
			c.Request = req

			files, err := multipartform.ParseUploadFiles(c)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none (parsed %d files)", len(files))
				}
				var inputErr *multipartform.InputError
				if !errors.As(err, &inputErr) {
					t.Fatalf("expected *multipartform.InputError, got %T: %v", err, err)
				}
				if inputErr.TooLarge != tc.wantTooLarge {
					t.Errorf("expected TooLarge=%v, got %v", tc.wantTooLarge, inputErr.TooLarge)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(files) != tc.wantFileCount {
				t.Errorf("expected %d files, got %d", tc.wantFileCount, len(files))
			}
		})
	}
}
