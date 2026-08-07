package service_test

import (
	"context"
	"errors"
	"testing"

	"resumesearch/internal/domain"
	"resumesearch/internal/service"
)

func TestEmbedResumeUseCase_Run(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the repo/model wired for this case, plus the
		// resumeID Run is called with.
		setup func() (*fakeRepo, *fakeModel, string)

		wantErr        bool
		wantStatuses   []domain.Status
		wantChunkCount int
	}{
		{
			name: "happy path chunks, embeds, saves chunks, and marks DONE",
			setup: func() (*fakeRepo, *fakeModel, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", RawText: "some text to embed"}, nil
				}}
				return repo, &fakeModel{}, "abc"
			},
			wantStatuses:   []domain.Status{domain.StatusProcessing, domain.StatusDone},
			wantChunkCount: 1,
		},
		{
			name: "already-DONE resume is skipped (idempotent under Kafka redelivery)",
			setup: func() (*fakeRepo, *fakeModel, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusDone}, nil
				}}
				return repo, &fakeModel{}, "abc"
			},
			wantStatuses: nil,
		},
		{
			name: "already-FAILED resume is skipped",
			setup: func() (*fakeRepo, *fakeModel, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusFailed}, nil
				}}
				return repo, &fakeModel{}, "abc"
			},
			wantStatuses: nil,
		},
		{
			name: "a redriven resume already at stage=EMBED is NOT skipped — the guard is on Status, not Stage",
			setup: func() (*fakeRepo, *fakeModel, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusProcessing, Stage: domain.StageEmbed, RawText: "some text to embed"}, nil
				}}
				return repo, &fakeModel{}, "abc"
			},
			wantStatuses:   []domain.Status{domain.StatusProcessing, domain.StatusDone},
			wantChunkCount: 1,
		},
		{
			name: "embedding failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", RawText: "some text to embed"}, nil
				}}
				model := &fakeModel{EmbedFn: func(ctx context.Context, text string) ([]float32, error) {
					return nil, errors.New("embedding service down")
				}}
				return repo, model, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "SaveChunks failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, string) {
				repo := &fakeRepo{
					GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
						return domain.Resume{ID: "abc", RawText: "some text to embed"}, nil
					},
					SaveChunksFn: func(ctx context.Context, resumeID string, chunks []domain.Chunk) error {
						return errors.New("db unreachable")
					},
				}
				return repo, &fakeModel{}, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "DONE-write failure after chunks are saved propagates the error WITHOUT marking FAILED — the sweeper closes this crash-gap",
			setup: func() (*fakeRepo, *fakeModel, string) {
				repo := &fakeRepo{
					GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
						return domain.Resume{ID: "abc", RawText: "some text to embed"}, nil
					},
					UpdateStatusFn: func(ctx context.Context, id string, status domain.Status, errMsg string) error {
						if status == domain.StatusDone {
							return errors.New("db unreachable")
						}
						return nil
					},
				}
				return repo, &fakeModel{}, "abc"
			},
			wantErr:        true,
			wantStatuses:   []domain.Status{domain.StatusProcessing, domain.StatusDone}, // DONE was attempted, just failed — never downgraded to FAILED
			wantChunkCount: 1,                                                           // chunks were saved before the DONE write failed
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, model, resumeID := tc.setup()
			uc := service.NewEmbedResumeUseCase(repo, model)

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
			if tc.wantChunkCount > 0 && len(repo.SavedChunks) != tc.wantChunkCount {
				t.Errorf("saved chunks = %d, want %d", len(repo.SavedChunks), tc.wantChunkCount)
			}
		})
	}
}
