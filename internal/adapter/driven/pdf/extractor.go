package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"resumesearch/internal/constants"
)

// ErrNoExtractableText is returned when neither pdftotext nor the OCR
// fallback can find any usable text in the PDF — e.g. a low-quality scan
// or a genuinely blank page.
var ErrNoExtractableText = errors.New("no extractable text found in PDF")

// Extractor pulls text out of PDFs. It tries pdftotext first (fast, exact,
// works for any text-based PDF); if that yields too little text it falls
// back to rasterizing pages with pdftoppm and running tesseract OCR on
// each page image. Both are shelled out via os/exec rather than a cgo
// binding — see decisions.md.
type Extractor struct{}

func NewExtractor() *Extractor {
	return &Extractor{}
}

// ExtractText returns the best text it can find for path. It returns
// ErrNoExtractableText (wrapped) if the PDF has no usable text even after
// the OCR fallback.
func (e *Extractor) ExtractText(ctx context.Context, path string) (string, error) {
	text, err := extractWithPdftotext(ctx, path)
	if err != nil {
		return "", err
	}
	if hasExtractableText(text) {
		return text, nil
	}

	ocrText, err := extractWithOCR(ctx, path)
	if err != nil {
		return "", fmt.Errorf("ocr fallback for %s: %w", path, err)
	}
	if !hasExtractableText(ocrText) {
		return "", fmt.Errorf("%w: %s (tried pdftotext and OCR)", ErrNoExtractableText, path)
	}
	return ocrText, nil
}

// hasExtractableText reports whether s contains enough real text to be
// worth keeping. pdftotext emits a lone form-feed ("\f") per page for
// image-only PDFs; strings.TrimSpace treats that as whitespace, so a
// scanned page collapses to an empty trimmed string here, same as a
// truly empty extraction.
func hasExtractableText(s string) bool {
	return len(strings.TrimSpace(s)) >= constants.MinExtractedTextChars
}

// extractWithPdftotext runs `pdftotext -layout <path> -` and returns
// stdout. "-layout" preserves column/whitespace structure, which helps
// the LLM extraction step read resumes with multi-column layouts.
func extractWithPdftotext(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext failed for %s: %w (stderr: %s)", path, err, stderr.String())
	}
	return stdout.String(), nil
}

var pageNumberRe = regexp.MustCompile(`-(\d+)\.png$`)

// pageNumber extracts the trailing page number pdftoppm embeds in each
// output filename (e.g. "page-1.png", "page-01.png"). poppler's
// zero-padding width isn't guaranteed across versions, so page order is
// determined by parsing this number rather than by sorting filenames
// lexicographically.
func pageNumber(filename string) int {
	m := pageNumberRe.FindStringSubmatch(filename)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// extractWithOCR rasterizes the first constants.MaxOCRPages pages to PNGs
// and runs tesseract on each, in page order, then joins the per-page text
// with blank lines. Bounding the page count keeps OCR — on the order of
// a second per page — inside constants.ExtractStageTimeout even for long
// documents.
func extractWithOCR(ctx context.Context, path string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "resume-ocr-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "page")
	rasterize := exec.CommandContext(ctx, "pdftoppm",
		"-png", "-r", strconv.Itoa(constants.OCRRasterDPI),
		"-f", "1", "-l", strconv.Itoa(constants.MaxOCRPages),
		path, prefix)
	var rasterStderr bytes.Buffer
	rasterize.Stderr = &rasterStderr
	if err := rasterize.Run(); err != nil {
		return "", fmt.Errorf("pdftoppm failed for %s: %w (stderr: %s)", path, err, rasterStderr.String())
	}

	pages, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return "", fmt.Errorf("glob rasterized pages: %w", err)
	}
	sort.Slice(pages, func(i, j int) bool {
		return pageNumber(pages[i]) < pageNumber(pages[j])
	})

	pageTexts := make([]string, 0, len(pages))
	for _, page := range pages {
		cmd := exec.CommandContext(ctx, "tesseract", page, "stdout")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("tesseract failed for %s: %w (stderr: %s)", page, err, stderr.String())
		}
		pageTexts = append(pageTexts, stdout.String())
	}
	return strings.Join(pageTexts, "\n\n"), nil
}
