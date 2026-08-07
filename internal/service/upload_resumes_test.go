package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resumesearch/internal/domain"
	"resumesearch/internal/service"
)

func TestUploadResumes_Run(t *testing.T) {
	cases := []struct {
		name          string
		files         []service.UploadFile
		repo          *fakeRepo
		pub           *fakePublisher
		wantErr       bool
		wantFilenames []string // if set, checked against resp.Resumes in order
	}{
		{
			name:  "happy path writes files, creates rows, publishes events",
			files: []service.UploadFile{{Filename: "a.pdf", Content: []byte("pdf-bytes-a")}, {Filename: "b.pdf", Content: []byte("pdf-bytes-b")}},
			repo:  &fakeRepo{CreateResumeFn: assignSequentialID()},
			pub:   &fakePublisher{},
		},
		{
			name:  "duplicate filenames in one batch do not overwrite each other",
			files: []service.UploadFile{{Filename: "resume.pdf", Content: []byte("candidate-1")}, {Filename: "resume.pdf", Content: []byte("candidate-2")}},
			repo:  &fakeRepo{CreateResumeFn: assignSequentialID()},
			pub:   &fakePublisher{},
		},
		{
			name:          "path traversal filename is stripped down to its base name, not rejected outright",
			files:         []service.UploadFile{{Filename: "../../etc/passwd", Content: []byte("x")}},
			repo:          &fakeRepo{CreateResumeFn: assignSequentialID()},
			pub:           &fakePublisher{},
			wantFilenames: []string{"passwd"},
		},
		{
			name:    "filename with no base component (\"..\") is rejected",
			files:   []service.UploadFile{{Filename: "..", Content: []byte("x")}},
			repo:    &fakeRepo{CreateResumeFn: assignSequentialID()},
			pub:     &fakePublisher{},
			wantErr: true,
		},
		{
			name:  "repository failure propagates",
			files: []service.UploadFile{{Filename: "a.pdf", Content: []byte("x")}},
			repo: &fakeRepo{CreateResumeFn: func(ctx context.Context, r *domain.Resume) error {
				return errors.New("db down")
			}},
			pub:     &fakePublisher{},
			wantErr: true,
		},
		{
			name:  "publisher failure propagates",
			files: []service.UploadFile{{Filename: "a.pdf", Content: []byte("x")}},
			repo:  &fakeRepo{CreateResumeFn: assignSequentialID()},
			pub: &fakePublisher{PublishResumeIngestFn: func(ctx context.Context, resumeID string) error {
				return errors.New("broker unreachable")
			}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			uc := service.NewUploadResumesUseCase(tc.repo, tc.pub, dir)

			resp, err := uc.Run(context.Background(), tc.files)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Run failed: %v", err)
			}
			if resp.BatchID == "" {
				t.Error("expected non-empty batch ID")
			}
			if len(resp.Resumes) != len(tc.files) {
				t.Fatalf("expected %d resume refs, got %d", len(tc.files), len(resp.Resumes))
			}
			if len(tc.repo.CreatedResumes) != len(tc.files) {
				t.Fatalf("expected %d rows created, got %d", len(tc.files), len(tc.repo.CreatedResumes))
			}
			if len(tc.pub.Published) != len(tc.files) {
				t.Fatalf("expected %d events published, got %d", len(tc.files), len(tc.pub.Published))
			}

			absDir, err := filepath.Abs(dir)
			if err != nil {
				t.Fatalf("resolve temp dir: %v", err)
			}

			for i, created := range tc.repo.CreatedResumes {
				content, err := os.ReadFile(created.FilePath)
				if err != nil {
					t.Fatalf("expected file written at %s: %v", created.FilePath, err)
				}
				if string(content) != string(tc.files[i].Content) {
					t.Errorf("file %d content = %q, want %q (files with duplicate names must not overwrite each other)", i, content, tc.files[i].Content)
				}

				absPath, err := filepath.Abs(created.FilePath)
				if err != nil {
					t.Fatalf("resolve file path: %v", err)
				}
				if !strings.HasPrefix(absPath, absDir+string(filepath.Separator)) {
					t.Errorf("file %d written to %s, escaped storage dir %s", i, absPath, absDir)
				}
			}

			if tc.wantFilenames != nil {
				for i, want := range tc.wantFilenames {
					if resp.Resumes[i].Filename != want {
						t.Errorf("resume %d filename = %q, want %q", i, resp.Resumes[i].Filename, want)
					}
				}
			}
		})
	}
}

// assignSequentialID mimics what a real repository does: assign an ID as
// a side effect of CreateResume.
func assignSequentialID() func(ctx context.Context, r *domain.Resume) error {
	n := 0
	return func(ctx context.Context, r *domain.Resume) error {
		n++
		r.ID = fmt.Sprintf("generated-id-%d", n)
		return nil
	}
}

// failOnCall wraps a CreateResumeFn so its nth invocation (1-indexed) fails
// instead of delegating, simulating an infra failure partway through a
// batch after earlier files already succeeded.
func failOnCall(n int, wrapped func(ctx context.Context, r *domain.Resume) error) func(ctx context.Context, r *domain.Resume) error {
	call := 0
	return func(ctx context.Context, r *domain.Resume) error {
		call++
		if call == n {
			return errors.New("db unreachable")
		}
		return wrapped(ctx, r)
	}
}

// TestUploadResumes_Run_InvalidFilenameRejectsWholeBatchBeforeAnySideEffect
// is the regression test for the partial-batch-commit bug found in review:
// a bad filename anywhere in the batch used to leave every earlier file in
// the same request already written to disk, inserted into Postgres, and
// published to Kafka, with the client left holding a bare error and no
// batch ID to find those orphaned resumes. Filenames are now validated
// before anything touches disk/DB/Kafka, so the whole batch is rejected
// atomically.
func TestUploadResumes_Run_InvalidFilenameRejectsWholeBatchBeforeAnySideEffect(t *testing.T) {
	repo := &fakeRepo{CreateResumeFn: assignSequentialID()}
	pub := &fakePublisher{}
	dir := t.TempDir()
	uc := service.NewUploadResumesUseCase(repo, pub, dir)

	files := []service.UploadFile{
		{Filename: "good.pdf", Content: []byte("A")},
		{Filename: "..", Content: []byte("B")}, // rejected by sanitizeFilename
	}

	_, err := uc.Run(context.Background(), files)
	if err == nil {
		t.Fatal("expected error for invalid filename, got nil")
	}
	if len(repo.CreatedResumes) != 0 {
		t.Errorf("expected no resume rows created before validating the whole batch, got %d", len(repo.CreatedResumes))
	}
	if len(pub.Published) != 0 {
		t.Errorf("expected no Kafka events published before validating the whole batch, got %d", len(pub.Published))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read storage dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no batch directory written before validating the whole batch, got %v", entries)
	}
}

// TestUploadResumes_Run_PartialFailureReturnsPartialResponse is the
// regression test for the residual case: even after upfront filename
// validation, an infra failure (DB/Kafka unreachable) partway through a
// batch must not discard the batch ID and the resumes that already
// succeeded — the caller needs that information to find them.
func TestUploadResumes_Run_PartialFailureReturnsPartialResponse(t *testing.T) {
	repo := &fakeRepo{CreateResumeFn: failOnCall(2, assignSequentialID())}
	pub := &fakePublisher{}
	dir := t.TempDir()
	uc := service.NewUploadResumesUseCase(repo, pub, dir)

	files := []service.UploadFile{
		{Filename: "a.pdf", Content: []byte("A")},
		{Filename: "b.pdf", Content: []byte("B")},
	}

	resp, err := uc.Run(context.Background(), files)
	if err == nil {
		t.Fatal("expected error from the second file's CreateResume failure, got nil")
	}
	if resp.BatchID == "" {
		t.Error("expected a non-empty batch ID even on partial failure")
	}
	if len(resp.Resumes) != 1 {
		t.Fatalf("expected 1 resume ref for the file that succeeded before the failure, got %d: %+v", len(resp.Resumes), resp.Resumes)
	}
	if resp.Resumes[0].Filename != "a.pdf" {
		t.Errorf("expected the successful resume to be a.pdf, got %q", resp.Resumes[0].Filename)
	}
}
