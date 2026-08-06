package utils_test

import (
	"strings"
	"testing"

	"resumesearch/internal/utils"
)

func TestRecursiveSplit(t *testing.T) {
	longPara := strings.Repeat("word ", 400) // ~400 words

	cases := []struct {
		name      string
		text      string
		maxTokens int
		wantLen   int // -1 means "don't check exact count"
	}{
		{
			name:      "empty text yields no chunks",
			text:      "",
			maxTokens: 512,
			wantLen:   0,
		},
		{
			name:      "short text is a single chunk",
			text:      "hello world",
			maxTokens: 512,
			wantLen:   1,
		},
		{
			name:      "text exceeding max splits on paragraph boundaries",
			text:      longPara + "\n\n" + longPara + "\n\n" + longPara,
			maxTokens: 512,
			wantLen:   -1,
		},
		{
			name:      "single word far longer than max still returns one chunk",
			text:      "supercalifragilisticexpialidocious",
			maxTokens: 1,
			wantLen:   1,
		},
		{
			name:      "zero maxTokens returns nil instead of looping forever",
			text:      "some words here",
			maxTokens: 0,
			wantLen:   0,
		},
		{
			name:      "negative maxTokens returns nil instead of looping forever",
			text:      "some words here",
			maxTokens: -1,
			wantLen:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks := utils.RecursiveSplit(tc.text, tc.maxTokens)
			if tc.wantLen >= 0 && len(chunks) != tc.wantLen {
				t.Fatalf("got %d chunks, want %d: %v", len(chunks), tc.wantLen, chunks)
			}
			for _, c := range chunks {
				if wordCount := len(strings.Fields(c)); wordCount > tc.maxTokens && len(strings.Fields(c)) > 1 {
					t.Errorf("chunk exceeds max tokens: %d words (max %d)", wordCount, tc.maxTokens)
				}
			}
		})
	}
}

func TestRecursiveSplit_LongTextProducesMultipleChunks(t *testing.T) {
	para := strings.Repeat("word ", 400)
	text := para + "\n\n" + para + "\n\n" + para
	chunks := utils.RecursiveSplit(text, 512)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long text, got %d", len(chunks))
	}
}
