package service_test

import (
	"context"
	"errors"
	"testing"

	"resumesearch/internal/domain"
	"resumesearch/internal/service"
)

func TestClassifyResumeUseCase_Run(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the repo/model/publisher wired for this case, plus
		// the resumeID Run is called with.
		setup func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string)

		wantErr           bool
		wantStatuses      []domain.Status
		wantStageCalls    []string
		wantSkills        []string
		wantPublishCalled bool
	}{
		{
			name: "happy path normalizes skills, saves fields, advances stage, and publishes",
			setup: func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", RawText: "Go and PostgreSQL backend engineer, 5 years, Remote"}, nil
				}}
				return repo, &fakeModel{}, &fakeClassifiedPublisher{}, "abc"
			},
			wantStatuses:      []domain.Status{domain.StatusProcessing},
			wantStageCalls:    []string{domain.StageEmbed},
			wantSkills:        []string{"go", "postgres"},
			wantPublishCalled: true,
		},
		{
			name: "already-DONE resume is skipped (idempotent under Kafka redelivery)",
			setup: func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusDone}, nil
				}}
				return repo, &fakeModel{}, &fakeClassifiedPublisher{}, "abc"
			},
			wantStatuses: nil,
		},
		{
			name: "already-FAILED resume is skipped",
			setup: func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusFailed}, nil
				}}
				return repo, &fakeModel{}, &fakeClassifiedPublisher{}, "abc"
			},
			wantStatuses: nil,
		},
		{
			name: "LLM extraction failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", RawText: "some text"}, nil
				}}
				model := &fakeModel{ExtractFn: func(ctx context.Context, text string) (domain.ExtractedFields, error) {
					return domain.ExtractedFields{}, errors.New("model unavailable")
				}}
				return repo, model, &fakeClassifiedPublisher{}, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "SaveExtractedFields failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string) {
				repo := &fakeRepo{
					GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
						return domain.Resume{ID: "abc", RawText: "some text"}, nil
					},
					SaveExtractedFieldsFn: func(ctx context.Context, id string, fields domain.ExtractedFields) error {
						return errors.New("db unreachable")
					},
				}
				return repo, &fakeModel{}, &fakeClassifiedPublisher{}, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "AdvanceStage failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string) {
				repo := &fakeRepo{
					GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
						return domain.Resume{ID: "abc", RawText: "some text"}, nil
					},
					AdvanceStageFn: func(ctx context.Context, id string, stage string) error {
						return errors.New("db unreachable")
					},
				}
				return repo, &fakeModel{}, &fakeClassifiedPublisher{}, "abc"
			},
			wantErr:        true,
			wantStatuses:   []domain.Status{domain.StatusProcessing, domain.StatusFailed},
			wantStageCalls: []string{domain.StageEmbed}, // the call was attempted; it just failed
		},
		{
			name: "publish failure after a successful advance propagates the error WITHOUT marking FAILED",
			setup: func() (*fakeRepo, *fakeModel, *fakeClassifiedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", RawText: "some text"}, nil
				}}
				publisher := &fakeClassifiedPublisher{PublishResumeClassifiedFn: func(ctx context.Context, resumeID string) error {
					return errors.New("kafka unreachable")
				}}
				return repo, &fakeModel{}, publisher, "abc"
			},
			wantErr:           true,
			wantStatuses:      []domain.Status{domain.StatusProcessing}, // no FAILED write
			wantStageCalls:    []string{domain.StageEmbed},              // stage already advanced before publish was attempted
			wantPublishCalled: true,                                     // publish was attempted, it just failed
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, model, publisher, resumeID := tc.setup()
			uc := service.NewClassifyResumeUseCase(repo, model, publisher)

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
			if len(repo.StageCalls) != len(tc.wantStageCalls) {
				t.Fatalf("stage calls = %v, want %v", repo.StageCalls, tc.wantStageCalls)
			}
			for i, want := range tc.wantStageCalls {
				if repo.StageCalls[i] != want {
					t.Errorf("stage call %d = %s, want %s", i, repo.StageCalls[i], want)
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
			gotPublishCalled := len(publisher.Published) > 0
			if gotPublishCalled != tc.wantPublishCalled {
				t.Errorf("publish called = %v, want %v", gotPublishCalled, tc.wantPublishCalled)
			}
		})
	}
}
