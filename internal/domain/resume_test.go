package domain_test

import (
	"errors"
	"testing"

	"resumesearch/internal/domain"
)

func TestStatusValues(t *testing.T) {
	cases := []struct {
		name   string
		status domain.Status
		want   string
	}{
		{"pending", domain.StatusPending, "PENDING"},
		{"processing", domain.StatusProcessing, "PROCESSING"},
		{"done", domain.StatusDone, "DONE"},
		{"failed", domain.StatusFailed, "FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.status) != tc.want {
				t.Errorf("got %q, want %q", string(tc.status), tc.want)
			}
		})
	}
}

func TestResumeZeroValueIsPending(t *testing.T) {
	var r domain.Resume
	if r.Status != "" {
		t.Errorf("expected zero-value Resume.Status to be empty, got %q", r.Status)
	}
}

func TestErrNotFound_IsDistinctSentinel(t *testing.T) {
	if !errors.Is(domain.ErrNotFound, domain.ErrNotFound) {
		t.Error("expected ErrNotFound to match itself via errors.Is")
	}
	if errors.Is(errors.New("some other error"), domain.ErrNotFound) {
		t.Error("expected an unrelated error not to match ErrNotFound")
	}
}
