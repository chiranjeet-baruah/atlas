package service_test

import (
	"context"
	"errors"
	"testing"

	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)

func TestSearchResumes_Run(t *testing.T) {
	minYears := 3.0

	cases := []struct {
		name  string
		repo  *fakeRepo
		model *fakeModel
		req   dto.SearchRequest

		wantErr        bool
		wantVecLen     int
		wantSkills     []string
		wantMinYears   *float64
		wantLocation   string
		wantResultsLen int
	}{
		{
			name: "embeds query, normalizes skills, forwards filters, maps results",
			repo: &fakeRepo{SearchFn: func(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error) {
				return []domain.SearchResult{{Resume: domain.Resume{ID: "r1", Filename: "a.pdf", Skills: []string{"go"}}, BestDistance: 0.1}}, nil
			}},
			model:          &fakeModel{EmbedFn: func(ctx context.Context, text string) ([]float32, error) { return []float32{0.5, 0.5}, nil }},
			req:            dto.SearchRequest{Query: "backend engineer", RequiredSkills: []string{"Go", "PostgreSQL"}, MinYears: &minYears, Location: "Remote"},
			wantVecLen:     2,
			wantSkills:     []string{"go", "postgres"},
			wantMinYears:   &minYears,
			wantLocation:   "Remote",
			wantResultsLen: 1,
		},
		{
			name: "no matches returns an empty, non-nil results slice",
			repo: &fakeRepo{SearchFn: func(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error) {
				return nil, nil
			}},
			model:          &fakeModel{},
			req:            dto.SearchRequest{Query: "nothing matches this"},
			wantResultsLen: 0,
		},
		{
			name: "embedding failure propagates without calling repository",
			repo: &fakeRepo{SearchFn: func(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error) {
				t.Fatal("Search must not be called when embedding fails")
				return nil, nil
			}},
			model:   &fakeModel{EmbedFn: func(ctx context.Context, text string) ([]float32, error) { return nil, errors.New("model unavailable") }},
			req:     dto.SearchRequest{Query: "backend engineer"},
			wantErr: true,
		},
		{
			name: "repository search failure propagates",
			repo: &fakeRepo{SearchFn: func(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error) {
				return nil, errors.New("db unreachable")
			}},
			model:   &fakeModel{},
			req:     dto.SearchRequest{Query: "backend engineer"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := service.NewSearchResumesUseCase(tc.repo, tc.model)
			resp, err := uc.Run(context.Background(), tc.req)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if resp.Results == nil {
				t.Error("expected non-nil Results slice")
			}
			if len(resp.Results) != tc.wantResultsLen {
				t.Fatalf("got %d results, want %d", len(resp.Results), tc.wantResultsLen)
			}
			if tc.wantVecLen > 0 {
				if len(tc.repo.SearchGotVec) != tc.wantVecLen {
					t.Errorf("got query vector len %d, want %d", len(tc.repo.SearchGotVec), tc.wantVecLen)
				}
			}
			if tc.wantSkills != nil {
				got := tc.repo.SearchGotFilters.RequiredSkills
				if len(got) != len(tc.wantSkills) {
					t.Fatalf("got skills %v, want %v", got, tc.wantSkills)
				}
				for i, want := range tc.wantSkills {
					if got[i] != want {
						t.Errorf("skill %d = %s, want %s", i, got[i], want)
					}
				}
			}
			if tc.wantMinYears != nil {
				got := tc.repo.SearchGotFilters.MinYears
				if got == nil || *got != *tc.wantMinYears {
					t.Errorf("got MinYears %v, want %v", got, *tc.wantMinYears)
				}
			}
			if tc.wantLocation != "" && tc.repo.SearchGotFilters.Location != tc.wantLocation {
				t.Errorf("got Location %q, want %q", tc.repo.SearchGotFilters.Location, tc.wantLocation)
			}
		})
	}
}
