package pdf_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"resumesearch/internal/adapter/driven/pdf"
)

// requireOCRTools skips the calling test on any machine missing one of the
// three CLIs the OCR fallback path needs, matching the pdftotext-only skip
// pattern already used by TestExtractText below.
func requireOCRTools(t *testing.T) {
	for _, bin := range []string{"pdftotext", "pdftoppm", "tesseract"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed on this machine — install poppler-utils/tesseract-ocr to run this test", bin)
		}
	}
}

func TestExtractText(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed on this machine — install poppler-utils to run this test")
	}

	cases := []struct {
		name    string
		path    string
		wantErr bool
		want    string
	}{
		{
			name: "text-based PDF extracts non-empty text",
			path: "testdata/sample.pdf",
			want: "John Doe",
		},
		{
			name:    "missing file returns an error",
			path:    "testdata/does-not-exist.pdf",
			wantErr: true,
		},
	}

	e := pdf.NewExtractor()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, err := e.ExtractText(context.Background(), tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractText failed: %v", err)
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("expected extracted text to contain %q, got %q", tc.want, text)
			}
		})
	}
}

func TestExtractText_OCRFallback(t *testing.T) {
	requireOCRTools(t)

	cases := []struct {
		name    string
		path    string
		wantErr error // nil means "any non-empty text is acceptable"
	}{
		{
			name: "scanned PDF with no text layer recovers text via OCR",
			path: "testdata/scanned.pdf",
		},
		{
			name:    "blank page with nothing to OCR returns ErrNoExtractableText",
			path:    "testdata/blank.pdf",
			wantErr: pdf.ErrNoExtractableText,
		},
	}

	e := pdf.NewExtractor()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, err := e.ExtractText(context.Background(), tc.path)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ExtractText(%s) error = %v, want %v", tc.path, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractText(%s) failed: %v", tc.path, err)
			}
			if strings.TrimSpace(text) == "" {
				t.Fatalf("ExtractText(%s): expected OCR fallback to produce non-empty text, got empty", tc.path)
			}
		})
	}
}

func TestExtractText_RespectsCanceledContext(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed on this machine — install poppler-utils to run this test")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e := pdf.NewExtractor()
	if _, err := e.ExtractText(ctx, "testdata/sample.pdf"); err == nil {
		t.Fatal("expected error for already-canceled context, got nil")
	}
}
