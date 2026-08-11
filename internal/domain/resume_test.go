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

func TestErrStatusNotRecorded_IsDistinctSentinel(t *testing.T) {
	if !errors.Is(domain.ErrStatusNotRecorded, domain.ErrStatusNotRecorded) {
		t.Error("expected ErrStatusNotRecorded to match itself via errors.Is")
	}
	if errors.Is(errors.New("some other error"), domain.ErrStatusNotRecorded) {
		t.Error("expected an unrelated error not to match ErrStatusNotRecorded")
	}
	if errors.Is(domain.ErrNotFound, domain.ErrStatusNotRecorded) {
		t.Error("expected ErrNotFound and ErrStatusNotRecorded to be distinct sentinels")
	}
}

func TestStageValues(t *testing.T) {
	cases := []struct {
		name  string
		stage string
		want  string
	}{
		{"extract", domain.StageExtract, "EXTRACT"},
		{"classify", domain.StageClassify, "CLASSIFY"},
		{"embed", domain.StageEmbed, "EMBED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stage != tc.want {
				t.Errorf("got %q, want %q", tc.stage, tc.want)
			}
		})
	}
}

func TestStatus_IsTerminal(t *testing.T) {
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
			if got := tc.status.IsTerminal(); got != tc.want {
				t.Errorf("%s.IsTerminal() = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
