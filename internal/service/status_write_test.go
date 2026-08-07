package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"resumesearch/internal/domain"
)

// statusOnlyRepo is a minimal ResumeRepository double scoped to this file.
// writeStatus is unexported, so its tests must live in this internal
// (package service) test file rather than alongside the shared fakeRepo in
// fakes_test.go, which belongs to the external service_test package.
type statusOnlyRepo struct {
	updateStatusFn func(ctx context.Context, id string, status domain.Status, errMsg string) error
	calls          []domain.Status
}

func (r *statusOnlyRepo) CreateResume(ctx context.Context, res *domain.Resume) error { return nil }
func (r *statusOnlyRepo) UpdateStatus(ctx context.Context, id string, status domain.Status, errMsg string) error {
	r.calls = append(r.calls, status)
	return r.updateStatusFn(ctx, id, status, errMsg)
}
func (r *statusOnlyRepo) AdvanceStage(ctx context.Context, id string, stage string) error { return nil }
func (r *statusOnlyRepo) SaveRawText(ctx context.Context, id string, rawText string) error {
	return nil
}
func (r *statusOnlyRepo) SaveExtractedFields(ctx context.Context, id string, fields domain.ExtractedFields) error {
	return nil
}
func (r *statusOnlyRepo) SaveChunks(ctx context.Context, resumeID string, chunks []domain.Chunk) error {
	return nil
}
func (r *statusOnlyRepo) GetByID(ctx context.Context, id string) (domain.Resume, error) {
	return domain.Resume{}, nil
}
func (r *statusOnlyRepo) GetByBatchID(ctx context.Context, batchID string) ([]domain.Resume, error) {
	return nil, nil
}
func (r *statusOnlyRepo) Search(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error) {
	return nil, nil
}
func (r *statusOnlyRepo) ClaimStaleForRedrive(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
	return nil, nil
}

// TestWriteStatus_SurvivesExpiredCallerContext locks in the fix for the
// stuck-PROCESSING bug: a status write made after the caller's ctx has
// already expired (e.g. a processing timeout) must still reach the
// repository, not fail immediately on the same expired deadline.
func TestWriteStatus_SurvivesExpiredCallerContext(t *testing.T) {
	cases := []struct {
		name    string
		makeCtx func() context.Context
	}{
		{
			name: "already-expired deadline",
			makeCtx: func() context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 0)
				cancel()
				return ctx
			},
		},
		{
			name: "already-canceled context",
			makeCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotCtxErr error
			repo := &statusOnlyRepo{updateStatusFn: func(ctx context.Context, id string, status domain.Status, errMsg string) error {
				gotCtxErr = ctx.Err()
				return nil
			}}

			if err := writeStatus(tc.makeCtx(), repo, "abc", domain.StatusFailed, "boom"); err != nil {
				t.Fatalf("writeStatus failed: %v", err)
			}
			if gotCtxErr != nil {
				t.Errorf("expected the context passed to UpdateStatus to still be live, got ctx.Err() = %v", gotCtxErr)
			}
			if len(repo.calls) != 1 || repo.calls[0] != domain.StatusFailed {
				t.Errorf("expected exactly one UpdateStatus(StatusFailed) call, got %v", repo.calls)
			}
		})
	}
}

func TestWriteStatus_PropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("db unreachable")
	repo := &statusOnlyRepo{updateStatusFn: func(ctx context.Context, id string, status domain.Status, errMsg string) error {
		return wantErr
	}}

	err := writeStatus(context.Background(), repo, "abc", domain.StatusDone, "")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected writeStatus to propagate the repository error, got %v", err)
	}
}

func TestWriteStatus_ValuesSurviveButDeadlineDoesNot(t *testing.T) {
	type ctxKey string
	const key ctxKey = "trace-id"

	parent := context.WithValue(context.Background(), key, "trace-123")
	parent, cancel := context.WithTimeout(parent, 0)
	cancel()

	var gotTraceID any
	var gotDeadlineOK bool
	repo := &statusOnlyRepo{updateStatusFn: func(ctx context.Context, id string, status domain.Status, errMsg string) error {
		gotTraceID = ctx.Value(key)
		_, gotDeadlineOK = ctx.Deadline()
		return nil
	}}

	if err := writeStatus(parent, repo, "abc", domain.StatusFailed, ""); err != nil {
		t.Fatalf("writeStatus failed: %v", err)
	}
	if gotTraceID != "trace-123" {
		t.Errorf("expected parent context values to survive, got %v", gotTraceID)
	}
	if !gotDeadlineOK {
		t.Error("expected writeStatus to give the write its own fresh deadline")
	}
}

func TestFailResume(t *testing.T) {
	procErr := errors.New("extract text: corrupt pdf")

	cases := []struct {
		name           string
		updateStatusFn func(ctx context.Context, id string, status domain.Status, errMsg string) error
		wantErrIs      error // checked with errors.Is against the returned error
		wantErrIsNot   error // checked to confirm it does NOT match, when set
	}{
		{
			name:           "status write succeeds — returns the original processing error unwrapped",
			updateStatusFn: func(ctx context.Context, id string, status domain.Status, errMsg string) error { return nil },
			wantErrIs:      procErr,
			wantErrIsNot:   domain.ErrStatusNotRecorded,
		},
		{
			name: "status write itself fails — returned error wraps both procErr and ErrStatusNotRecorded",
			updateStatusFn: func(ctx context.Context, id string, status domain.Status, errMsg string) error {
				return errors.New("db down")
			},
			wantErrIs: procErr,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &statusOnlyRepo{updateStatusFn: tc.updateStatusFn}

			err := failResume(context.Background(), repo, "abc", procErr)

			if !errors.Is(err, tc.wantErrIs) {
				t.Errorf("expected error to match %v via errors.Is, got %v", tc.wantErrIs, err)
			}
			if tc.wantErrIsNot != nil && errors.Is(err, tc.wantErrIsNot) {
				t.Errorf("expected error not to match %v via errors.Is, got %v", tc.wantErrIsNot, err)
			}
			if len(repo.calls) != 1 || repo.calls[0] != domain.StatusFailed {
				t.Errorf("expected exactly one UpdateStatus(StatusFailed) call, got %v", repo.calls)
			}
		})
	}
}

func TestFailResume_StatusWriteFailure_WrapsErrStatusNotRecorded(t *testing.T) {
	procErr := errors.New("llm extract: model unavailable")
	statusErr := errors.New("db down")
	repo := &statusOnlyRepo{updateStatusFn: func(ctx context.Context, id string, status domain.Status, errMsg string) error {
		return statusErr
	}}

	err := failResume(context.Background(), repo, "abc", procErr)

	if !errors.Is(err, domain.ErrStatusNotRecorded) {
		t.Errorf("expected error to match domain.ErrStatusNotRecorded via errors.Is, got %v", err)
	}
	if !errors.Is(err, procErr) {
		t.Errorf("expected error to still match the original processing error via errors.Is, got %v", err)
	}
}

func TestIsTerminal(t *testing.T) {
	cases := []struct {
		name   string
		status domain.Status
		want   bool
	}{
		{"pending is not terminal", domain.StatusPending, false},
		{"processing is not terminal", domain.StatusProcessing, false},
		{"done is terminal", domain.StatusDone, true},
		{"failed is terminal", domain.StatusFailed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTerminal(tc.status); got != tc.want {
				t.Errorf("isTerminal(%s) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
