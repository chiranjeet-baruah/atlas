package utils_test

import (
	"reflect"
	"testing"

	"resumesearch/internal/utils"
)

func TestNormalizeSkills(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "lowercases, trims, de-aliases, de-dupes",
			in:   []string{"Python3", "  Go  ", "PYTHON", "golang", "Postgres"},
			want: []string{"python", "go", "postgres"},
		},
		{
			name: "postgresql alias collapses onto postgres",
			in:   []string{"PostgreSQL", "postgres"},
			want: []string{"postgres"},
		},
		{
			name: "nil input yields empty non-nil slice",
			in:   nil,
			want: []string{},
		},
		{
			name: "blank entries are dropped",
			in:   []string{"", "   ", "go"},
			want: []string{"go"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := utils.NormalizeSkills(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
