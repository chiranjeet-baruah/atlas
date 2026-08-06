package pdf

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Extractor pulls text out of text-based PDFs by shelling out to
// pdftotext (poppler-utils). Chosen over a cgo/MuPDF binding for Docker
// build reliability — see decisions.md.
type Extractor struct{}

func NewExtractor() *Extractor {
	return &Extractor{}
}

// ExtractText runs `pdftotext -layout <path> -` and returns stdout.
// "-layout" preserves column/whitespace structure, which helps the LLM
// extraction step read resumes with multi-column layouts.
func (e *Extractor) ExtractText(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext failed for %s: %w (stderr: %s)", path, err, stderr.String())
	}
	return stdout.String(), nil
}
