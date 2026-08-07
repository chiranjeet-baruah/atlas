package dto_test

import (
	"encoding/json"
	"testing"

	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
)

func TestFromResume(t *testing.T) {
	cases := []struct {
		name string
		in   domain.Resume
		want dto.StatusResponse
	}{
		{
			name: "done resume",
			in:   domain.Resume{ID: "abc-123", Filename: "jane.pdf", Status: domain.StatusDone, Stage: domain.StageEmbed},
			want: dto.StatusResponse{ID: "abc-123", Filename: "jane.pdf", Status: "DONE", Stage: "EMBED"},
		},
		{
			name: "failed resume carries error message and its stuck stage",
			in:   domain.Resume{ID: "def-456", Filename: "bob.pdf", Status: domain.StatusFailed, Stage: domain.StageClassify, ErrorMessage: "corrupt pdf"},
			want: dto.StatusResponse{ID: "def-456", Filename: "bob.pdf", Status: "FAILED", Stage: "CLASSIFY", ErrorMessage: "corrupt pdf"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dto.FromResume(tc.in)
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestFromResumes_EmptyInputSerializesAsEmptyArrayNotNull(t *testing.T) {
	got := dto.FromResumes(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("expected JSON \"[]\", got %s", b)
	}
}

func TestFromSearchResult(t *testing.T) {
	sr := domain.SearchResult{
		Resume: domain.Resume{
			ID:              "abc-123",
			Filename:        "jane.pdf",
			Skills:          []string{"go", "postgres"},
			YearsExperience: 5,
			Location:        "Remote",
		},
		BestDistance: 0.12,
	}
	got := dto.FromSearchResult(sr)
	want := dto.SearchResultDTO{
		ID:              "abc-123",
		Filename:        "jane.pdf",
		Skills:          []string{"go", "postgres"},
		YearsExperience: 5,
		Location:        "Remote",
		Distance:        0.12,
	}
	if got.ID != want.ID || got.Distance != want.Distance || len(got.Skills) != len(want.Skills) {
		t.Errorf("unexpected SearchResultDTO: %+v", got)
	}
}

func TestFromSearchResults_EmptyInputSerializesAsEmptyArrayNotNull(t *testing.T) {
	got := dto.FromSearchResults(nil)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("expected JSON \"[]\", got %s", b)
	}
}
