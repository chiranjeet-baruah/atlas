package service_test

import (
	"context"
	"errors"
	"testing"

	"resumesearch/internal/domain"
	"resumesearch/internal/service"
)

func TestExtractResumeUseCase_Run(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the repo/extractor/publisher wired for this case,
		// plus the resumeID Run is called with.
		setup func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string)

		wantErr           bool
		wantStatuses      []domain.Status
		wantStageCalls    []string
		wantRawText       string
		wantPublishCalled bool
	}{
		{
			name: "happy path saves raw text, advances stage, and publishes",
			setup: func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
				}}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "Go and PostgreSQL backend engineer, 5 years, Remote", nil
				}}
				return repo, extractor, &fakeExtractedPublisher{}, "abc"
			},
			wantStatuses:      []domain.Status{domain.StatusProcessing},
			wantStageCalls:    []string{domain.StageClassify},
			wantRawText:       "Go and PostgreSQL backend engineer, 5 years, Remote",
			wantPublishCalled: true,
		},
		{
			name: "already-DONE resume is skipped (idempotent under Kafka redelivery)",
			setup: func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusDone}, nil
				}}
				return repo, &fakeExtractor{}, &fakeExtractedPublisher{}, "abc"
			},
			wantStatuses: nil,
		},
		{
			name: "already-FAILED resume is skipped — a poison-pill cutoff must stay terminal",
			setup: func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", Status: domain.StatusFailed}, nil
				}}
				return repo, &fakeExtractor{}, &fakeExtractedPublisher{}, "abc"
			},
			wantStatuses: nil,
		},
		{
			name: "extraction failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
				}}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "", errors.New("corrupt pdf")
				}}
				return repo, extractor, &fakeExtractedPublisher{}, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "SaveRawText failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string) {
				repo := &fakeRepo{
					GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
						return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
					},
					SaveRawTextFn: func(ctx context.Context, id, rawText string) error {
						return errors.New("db unreachable")
					},
				}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "some text", nil
				}}
				return repo, extractor, &fakeExtractedPublisher{}, "abc"
			},
			wantErr:      true,
			wantStatuses: []domain.Status{domain.StatusProcessing, domain.StatusFailed},
		},
		{
			name: "AdvanceStage failure marks FAILED and propagates error",
			setup: func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string) {
				repo := &fakeRepo{
					GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
						return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
					},
					AdvanceStageFn: func(ctx context.Context, id string, stage string) error {
						return errors.New("db unreachable")
					},
				}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "some text", nil
				}}
				return repo, extractor, &fakeExtractedPublisher{}, "abc"
			},
			wantErr:        true,
			wantStatuses:   []domain.Status{domain.StatusProcessing, domain.StatusFailed},
			wantStageCalls: []string{domain.StageClassify}, // the call was attempted; it just failed
		},
		{
			name: "publish failure after a successful advance propagates the error WITHOUT marking FAILED — the sweeper closes this crash-gap, not a FAILED write",
			setup: func() (*fakeRepo, *fakeExtractor, *fakeExtractedPublisher, string) {
				repo := &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
					return domain.Resume{ID: "abc", FilePath: "/data/x.pdf"}, nil
				}}
				extractor := &fakeExtractor{ExtractTextFn: func(ctx context.Context, path string) (string, error) {
					return "some text", nil
				}}
				publisher := &fakeExtractedPublisher{PublishResumeExtractedFn: func(ctx context.Context, resumeID string) error {
					return errors.New("kafka unreachable")
				}}
				return repo, extractor, publisher, "abc"
			},
			wantErr:           true,
			wantStatuses:      []domain.Status{domain.StatusProcessing}, // no FAILED write
			wantStageCalls:    []string{domain.StageClassify},           // stage already advanced before publish was attempted
			wantPublishCalled: true,                                     // publish was attempted, it just failed
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, extractor, publisher, resumeID := tc.setup()
			uc := service.NewExtractResumeUseCase(repo, extractor, publisher)

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
			if tc.wantRawText != "" && repo.SavedRawText != tc.wantRawText {
				t.Errorf("saved raw text = %q, want %q", repo.SavedRawText, tc.wantRawText)
			}
			gotPublishCalled := len(publisher.Published) > 0
			if gotPublishCalled != tc.wantPublishCalled {
				t.Errorf("publish called = %v, want %v", gotPublishCalled, tc.wantPublishCalled)
			}
		})
	}
}
