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

// wantFile is the expected filename/content pair for a successfully parsed
// upload, used to assert that ParseUploadFiles didn't swap or scramble which
// field gets which value.
type wantFile struct {
	filename string
	content  string
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
		if _, err := part.Write([]byte("content-of-" + name)); err != nil {
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
		malformedBody bool
		wantErr       bool
		wantTooLarge  bool
		wantFiles     []wantFile
	}{
		{
			name:      "single file parses",
			filenames: []string{"a.pdf"},
			wantFiles: []wantFile{{filename: "a.pdf", content: "content-of-a.pdf"}},
		},
		{
			name:      "multiple files under the same field all parse",
			filenames: []string{"a.pdf", "b.pdf"},
			wantFiles: []wantFile{
				{filename: "a.pdf", content: "content-of-a.pdf"},
				{filename: "b.pdf", content: "content-of-b.pdf"},
			},
		},
		{name: "zero files is an InputError, not TooLarge", filenames: []string{}, wantErr: true, wantTooLarge: false},
		{name: "over the file-count limit is an InputError, not TooLarge", filenames: tooManyFilenames, wantErr: true, wantTooLarge: false},
		{name: "over the body-size limit is an InputError with TooLarge set", filenames: []string{"huge.pdf"}, oversizedFile: true, wantErr: true, wantTooLarge: true},
		{name: "malformed multipart body is an InputError, not TooLarge", malformedBody: true, wantErr: true, wantTooLarge: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body *bytes.Buffer
			var contentType string
			switch {
			case tc.malformedBody:
				// A boundary is declared in the Content-Type header, but the
				// body is plain garbage that doesn't match it at all — so
				// c.MultipartForm() fails to parse it as a multipart form,
				// as opposed to parsing fine and then exceeding a limit.
				body = bytes.NewBufferString("this is not a valid multipart body at all, just garbage bytes")
				contentType = "multipart/form-data; boundary=x"
			case tc.oversizedFile:
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
			default:
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
			if len(files) != len(tc.wantFiles) {
				t.Fatalf("expected %d files, got %d", len(tc.wantFiles), len(files))
			}
			got := make(map[string]string, len(files))
			for _, f := range files {
				got[f.Filename] = string(f.Content)
			}
			for _, wf := range tc.wantFiles {
				gotContent, ok := got[wf.filename]
				if !ok {
					t.Errorf("expected file %q in result, not found among %v", wf.filename, keys(got))
					continue
				}
				if gotContent != wf.content {
					t.Errorf("file %q: expected content %q, got %q", wf.filename, wf.content, gotContent)
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
