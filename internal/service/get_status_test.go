package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"resumesearch/internal/domain"
	"resumesearch/internal/service"
)

func TestGetStatus_ByID(t *testing.T) {
	cases := []struct {
		name    string
		repo    *fakeRepo
		id      string
		wantErr error // checked with errors.Is; nil means "no error expected"
		want    struct{ status, filename string }
	}{
		{
			name: "done resume returns its status",
			repo: &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
				return domain.Resume{ID: "r1", Filename: "a.pdf", Status: domain.StatusDone}, nil
			}},
			id:   "r1",
			want: struct{ status, filename string }{"DONE", "a.pdf"},
		},
		{
			name: "missing resume propagates domain.ErrNotFound",
			repo: &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
				return domain.Resume{}, domain.ErrNotFound
			}},
			id:      "missing",
			wantErr: domain.ErrNotFound,
		},
		{
			name: "repository failure propagates as a distinct, non-ErrNotFound error",
			repo: &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
				return domain.Resume{}, errors.New("db unreachable")
			}},
			id:      "r1",
			wantErr: errors.New("db unreachable"), // sentinel: any non-nil, non-ErrNotFound error
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := service.NewGetStatusUseCase(tc.repo)
			got, err := uc.ByID(context.Background(), tc.id)

			if tc.wantErr != nil {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if errors.Is(tc.wantErr, domain.ErrNotFound) != errors.Is(err, domain.ErrNotFound) {
					t.Errorf("errors.Is(err, ErrNotFound) = %v, want %v", errors.Is(err, domain.ErrNotFound), errors.Is(tc.wantErr, domain.ErrNotFound))
				}
				return
			}
			if err != nil {
				t.Fatalf("ByID failed: %v", err)
			}
			if got.Status != tc.want.status || got.Filename != tc.want.filename {
				t.Errorf("got %+v, want status=%s filename=%s", got, tc.want.status, tc.want.filename)
			}
		})
	}
}

func TestGetStatus_FileByID(t *testing.T) {
	cases := []struct {
		name    string
		repo    *fakeRepo
		id      string
		wantErr error // checked with errors.Is; nil means "no error expected"
		want    struct{ filename, filePath string }
	}{
		{
			name: "resume returns its filename and on-disk path",
			repo: &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
				return domain.Resume{ID: "r1", Filename: "a.pdf", FilePath: "/data/resumes/batch-1/0_a.pdf"}, nil
			}},
			id:   "r1",
			want: struct{ filename, filePath string }{"a.pdf", "/data/resumes/batch-1/0_a.pdf"},
		},
		{
			name: "missing resume propagates domain.ErrNotFound",
			repo: &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
				return domain.Resume{}, domain.ErrNotFound
			}},
			id:      "missing",
			wantErr: domain.ErrNotFound,
		},
		{
			name: "repository failure propagates as a distinct, non-ErrNotFound error",
			repo: &fakeRepo{GetByIDFn: func(ctx context.Context, id string) (domain.Resume, error) {
				return domain.Resume{}, errors.New("db unreachable")
			}},
			id:      "r1",
			wantErr: errors.New("db unreachable"), // sentinel: any non-nil, non-ErrNotFound error
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := service.NewGetStatusUseCase(tc.repo)
			got, err := uc.FileByID(context.Background(), tc.id)

			if tc.wantErr != nil {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if errors.Is(tc.wantErr, domain.ErrNotFound) != errors.Is(err, domain.ErrNotFound) {
					t.Errorf("errors.Is(err, ErrNotFound) = %v, want %v", errors.Is(err, domain.ErrNotFound), errors.Is(tc.wantErr, domain.ErrNotFound))
				}
				return
			}
			if err != nil {
				t.Fatalf("FileByID failed: %v", err)
			}
			if got.Filename != tc.want.filename || got.FilePath != tc.want.filePath {
				t.Errorf("got %+v, want filename=%s filePath=%s", got, tc.want.filename, tc.want.filePath)
			}
		})
	}
}

func TestGetStatus_ByBatchID(t *testing.T) {
	cases := []struct {
		name            string
		repo            *fakeRepo
		batchID         string
		wantErr         bool
		wantErrNotFound bool // if true, err must satisfy errors.Is(err, domain.ErrNotFound)
		wantCount       int
		wantErrMsgs     []string // ErrorMessage per resume, in order
	}{
		{
			name: "batch with mixed statuses returns every resume, error messages preserved",
			repo: &fakeRepo{GetByBatchIDFn: func(ctx context.Context, batchID string) ([]domain.Resume, error) {
				return []domain.Resume{
					{ID: "r1", Filename: "a.pdf", Status: domain.StatusDone},
					{ID: "r2", Filename: "b.pdf", Status: domain.StatusFailed, ErrorMessage: "bad pdf"},
				}, nil
			}},
			batchID:     "batch-1",
			wantCount:   2,
			wantErrMsgs: []string{"", "bad pdf"},
		},
		{
			name: "unknown batch (zero resumes) propagates domain.ErrNotFound, not an empty list",
			repo: &fakeRepo{GetByBatchIDFn: func(ctx context.Context, batchID string) ([]domain.Resume, error) {
				return nil, nil
			}},
			batchID:         "batch-empty",
			wantErr:         true,
			wantErrNotFound: true,
		},
		{
			name: "repository failure propagates as a distinct, non-ErrNotFound error",
			repo: &fakeRepo{GetByBatchIDFn: func(ctx context.Context, batchID string) ([]domain.Resume, error) {
				return nil, fmt.Errorf("db unreachable")
			}},
			batchID: "batch-1",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := service.NewGetStatusUseCase(tc.repo)
			got, err := uc.ByBatchID(context.Background(), tc.batchID)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if got := errors.Is(err, domain.ErrNotFound); got != tc.wantErrNotFound {
					t.Errorf("errors.Is(err, ErrNotFound) = %v, want %v", got, tc.wantErrNotFound)
				}
				return
			}
			if err != nil {
				t.Fatalf("ByBatchID failed: %v", err)
			}
			if got.BatchID != tc.batchID {
				t.Errorf("got BatchID %q, want %q", got.BatchID, tc.batchID)
			}
			if got.Resumes == nil {
				t.Error("expected non-nil Resumes slice")
			}
			if len(got.Resumes) != tc.wantCount {
				t.Fatalf("got %d resumes, want %d", len(got.Resumes), tc.wantCount)
			}
			for i, want := range tc.wantErrMsgs {
				if got.Resumes[i].ErrorMessage != want {
					t.Errorf("resume %d ErrorMessage = %q, want %q", i, got.Resumes[i].ErrorMessage, want)
				}
			}
		})
	}
}

func TestGetStatus_ListBatches(t *testing.T) {
	cases := []struct {
		name      string
		repo      *fakeRepo
		wantErr   bool
		wantCount int
		wantFirst string // BatchID of the first returned summary, checked when wantCount > 0
	}{
		{
			name: "no batches yet returns an empty slice, not an error",
			repo: &fakeRepo{ListBatchesFn: func(ctx context.Context) ([]domain.BatchSummary, error) {
				return nil, nil
			}},
			wantCount: 0,
		},
		{
			name: "multiple batches map through in the order the repo returned them",
			repo: &fakeRepo{ListBatchesFn: func(ctx context.Context) ([]domain.BatchSummary, error) {
				return []domain.BatchSummary{
					{BatchID: "batch-2", Total: 3, Done: 1, Failed: 2},
					{BatchID: "batch-1", Total: 5, Pending: 5},
				}, nil
			}},
			wantCount: 2,
			wantFirst: "batch-2",
		},
		{
			name: "repository failure propagates",
			repo: &fakeRepo{ListBatchesFn: func(ctx context.Context) ([]domain.BatchSummary, error) {
				return nil, errors.New("db unreachable")
			}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := service.NewGetStatusUseCase(tc.repo)
			got, err := uc.ListBatches(context.Background())

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListBatches failed: %v", err)
			}
			if got == nil {
				t.Error("expected non-nil slice")
			}
			if len(got) != tc.wantCount {
				t.Fatalf("got %d batches, want %d", len(got), tc.wantCount)
			}
			if tc.wantCount > 0 && got[0].BatchID != tc.wantFirst {
				t.Errorf("got first BatchID %q, want %q", got[0].BatchID, tc.wantFirst)
			}
		})
	}
}
