package pdf_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"resumesearch/internal/adapter/driven/pdf"
)

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
