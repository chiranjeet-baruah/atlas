package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
	"resumesearch/internal/service"
)

func TestRedriveSweepUseCase_Run(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the repo and the three stage publishers wired for
		// this case.
		setup func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher)

		wantErr                 bool
		wantIngestPublished     []string
		wantExtractedPublished  []string
		wantClassifiedPublished []string
		wantStatuses            []domain.Status
	}{
		{
			name: "empty claim batch is a no-op",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
		},
		{
			name: "resume stuck at stage EXTRACT is redriven to the ingest topic",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return []domain.Resume{{ID: "abc", Stage: domain.StageExtract, RedriveCount: 1}}, nil
				}}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantIngestPublished: []string{"abc"},
		},
		{
			name: "resume stuck at stage CLASSIFY is redriven to the extracted topic",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return []domain.Resume{{ID: "abc", Stage: domain.StageClassify, RedriveCount: 2}}, nil
				}}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantExtractedPublished: []string{"abc"},
		},
		{
			name: "resume stuck at stage EMBED is redriven to the classified topic",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return []domain.Resume{{ID: "abc", Stage: domain.StageEmbed, RedriveCount: 3}}, nil
				}}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantClassifiedPublished: []string{"abc"},
		},
		{
			name: "redrive count pushed past MaxRedrives by this claim marks FAILED instead of republishing",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return []domain.Resume{{ID: "abc", Stage: domain.StageClassify, RedriveCount: constants.MaxRedrives + 1}}, nil
				}}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantStatuses: []domain.Status{domain.StatusFailed},
		},
		{
			name: "a resume at MaxRedrives exactly (not yet over) is still redriven, not failed",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return []domain.Resume{{ID: "abc", Stage: domain.StageExtract, RedriveCount: constants.MaxRedrives}}, nil
				}}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantIngestPublished: []string{"abc"},
		},
		{
			name: "unrecognized stage returns an error instead of silently dropping the resume",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return []domain.Resume{{ID: "abc", Stage: "BOGUS", RedriveCount: 1}}, nil
				}}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantErr: true,
		},
		{
			name: "ClaimStaleForRedrive failure propagates",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return nil, errors.New("db unreachable")
				}}
				return repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantErr: true,
		},
		{
			name: "one resume's publish failure does not block redriving the rest of the batch",
			setup: func() (*fakeRepo, *fakePublisher, *fakeExtractedPublisher, *fakeClassifiedPublisher) {
				repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
					return []domain.Resume{
						{ID: "fails", Stage: domain.StageExtract, RedriveCount: 1},
						{ID: "succeeds", Stage: domain.StageExtract, RedriveCount: 1},
					}, nil
				}}
				publisher := &fakePublisher{PublishResumeIngestFn: func(ctx context.Context, resumeID string) error {
					if resumeID == "fails" {
						return errors.New("kafka unreachable")
					}
					return nil
				}}
				return repo, publisher, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{}
			},
			wantErr:             true,
			wantIngestPublished: []string{"fails", "succeeds"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, ingestPub, extractedPub, classifiedPub := tc.setup()
			uc := service.NewRedriveSweepUseCase(repo, ingestPub, extractedPub, classifiedPub)

			err := uc.Run(context.Background())

			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertPublished(t, "ingest", ingestPub.Published, tc.wantIngestPublished)
			assertPublished(t, "extracted", extractedPub.Published, tc.wantExtractedPublished)
			assertPublished(t, "classified", classifiedPub.Published, tc.wantClassifiedPublished)
			if len(repo.StatusCalls) != len(tc.wantStatuses) {
				t.Fatalf("status calls = %v, want %v", repo.StatusCalls, tc.wantStatuses)
			}
			for i, want := range tc.wantStatuses {
				if repo.StatusCalls[i] != want {
					t.Errorf("status call %d = %s, want %s", i, repo.StatusCalls[i], want)
				}
			}
		})
	}
}

func assertPublished(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s published = %v, want %v", label, got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("%s published[%d] = %s, want %s", label, i, got[i], w)
		}
	}
}

func TestRedriveSweepUseCase_Run_ClaimsWithConstantsFromConfiguration(t *testing.T) {
	var gotStaleAfter time.Duration
	var gotMaxRedrives, gotLimit int
	repo := &fakeRepo{ClaimStaleForRedriveFn: func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
		gotStaleAfter, gotMaxRedrives, gotLimit = staleAfter, maxRedrives, limit
		return nil, nil
	}}
	uc := service.NewRedriveSweepUseCase(repo, &fakePublisher{}, &fakeExtractedPublisher{}, &fakeClassifiedPublisher{})

	if err := uc.Run(context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if gotStaleAfter != constants.SweepStaleAfter {
		t.Errorf("staleAfter = %v, want %v", gotStaleAfter, constants.SweepStaleAfter)
	}
	if gotMaxRedrives != constants.MaxRedrives {
		t.Errorf("maxRedrives = %d, want %d", gotMaxRedrives, constants.MaxRedrives)
	}
	if gotLimit != constants.SweepBatchSize {
		t.Errorf("limit = %d, want %d", gotLimit, constants.SweepBatchSize)
	}
}
