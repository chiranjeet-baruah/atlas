package pdf

import (
	"strings"
	"testing"
)

func TestHasExtractableText(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{name: "empty string", text: "", want: false},
		{name: "lone form feed (scanned page marker)", text: "\f", want: false},
		{name: "whitespace and form feeds only", text: "  \f\n\t\f  ", want: false},
		{name: "short junk below threshold", text: "abc", want: false},
		{name: "real resume text clears threshold", text: strings.Repeat("Experienced backend engineer. ", 5), want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasExtractableText(tc.text); got != tc.want {
				t.Errorf("hasExtractableText(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
