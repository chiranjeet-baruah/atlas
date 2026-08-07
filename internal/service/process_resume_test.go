package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"resumesearch/internal/adapter/driven/pdf"
	"resumesearch/internal/domain"
	"resumesearch/internal/service"
)

func TestProcessResume_Run(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the repo/model/extractor wired for this case, plus
		// the resumeID Run is called with.
		setup func() (*fakeRepo, *fakeModel, *fakeExtractor, string)

		wantErr            bool
		wantStatuses       []domain.Status
		wantSkills         []string
		wantChunkCount     int
		wantModelNotCalled bool
	}{
		{
			name: "happy path processes, normalizes skills, chunks, and marks DONE",
			setup: func() (*fakeRepo, *fakeModel, *fakeExtractor, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
				}}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "Go and PostgreSQL backend engineer, 5 years, Remote", nil
				}}
				return repo, &fakeModel{}, extractor, "abc"
			},
			wantStatuses:   []domain.Status{domain.StatusProcessing, domain.StatusDone},
			wantSkills:     []string{"go", "postgres"},
			wantChunkCount: 1,
		},
		{
			name: "already-DONE resume is skipped (idempotent under Kafka redelivery)",
			setup: func() (*fakeRepo, *fakeModel, *fakeExtractor, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusDone}, nil
				}}
				return repo, &fakeModel{}, &fakeExtractor{}, "abc"
			},
			wantStatuses: nil, // no UpdateStatus call at all — Run returns before touching status
		},
		{
			name: "text extraction failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, *fakeExtractor, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
				}}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "", errors.New("corrupt pdf")
				}}
				return repo, &fakeModel{}, extractor, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "no-extractable-text error marks FAILED without calling the model",
			setup: func() (*fakeRepo, *fakeModel, *fakeExtractor, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/scanned.pdf"}, nil
				}}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "", fmt.Errorf("%w: /data/scanned.pdf (tried pdftotext and OCR)", pdf.ErrNoExtractableText)
				}}
				return repo, &fakeModel{}, extractor, "abc"
			},
			wantErr:            true,
			wantStatuses:       []domain.Status{domain.StatusProcessing, domain.StatusFailed},
			wantModelNotCalled: true,
		},
		{
			name: "LLM extraction failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, *fakeExtractor, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
				}}
				model := &fakeModel{ExtractFn: func(ctx context.Context, text string) (domain.ExtractedFields, error) {
					return domain.ExtractedFields{}, errors.New("model unavailable")
				}}
				return repo, model, &fakeExtractor{}, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "embedding failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, *fakeExtractor, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
				}}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "some text to embed", nil
				}}
				model := &fakeModel{EmbedFn: func(ctx context.Context, text string) ([]float32, error) {
					return nil, errors.New("embedding service down")
				}}
				return repo, model, extractor, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "GetByID failure propagates without touching status",
			setup: func() (*fakeRepo, *fakeModel, *fakeExtractor, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{}, errors.New("db unreachable")
				}}
				return repo, &fakeModel{}, &fakeExtractor{}, "abc"
			},
			wantErr:      true,
			wantStatuses: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, model, extractor, resumeID := tc.setup()
			uc := service.NewProcessResumeUseCase(repo, model, extractor)

			err := uc.Run(context.Background(), resumeID)

			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(repo.StatusCalls) != len(tc.wantStatuses) {
				t.Fatalf("status calls = %v, want %v", repo.StatusCalls, tc.wantStatuses)
			}
			for i, want := range tc.wantStatuses {
				if repo.StatusCalls[i] != want {
					t.Errorf("status call %d = %s, want %s", i, repo.StatusCalls[i], want)
				}
			}
			if tc.wantSkills != nil {
				if len(repo.SavedFields.Skills) != len(tc.wantSkills) {
					t.Fatalf("saved skills = %v, want %v", repo.SavedFields.Skills, tc.wantSkills)
				}
				for i, want := range tc.wantSkills {
					if repo.SavedFields.Skills[i] != want {
						t.Errorf("saved skill %d = %s, want %s", i, repo.SavedFields.Skills[i], want)
					}
				}
			}
			if tc.wantChunkCount > 0 && len(repo.SavedChunks) != tc.wantChunkCount {
				t.Errorf("saved chunks = %d, want %d", len(repo.SavedChunks), tc.wantChunkCount)
			}
			if tc.wantModelNotCalled && model.ExtractCalled {
				t.Error("expected model.Extract not to be called")
			}
		})
	}
}
