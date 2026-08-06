package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
)

type UploadFile struct {
	Filename string
	Content  []byte
}

type UploadResumesUseCase struct {
	repo       ResumeRepository
	publisher  EventPublisher
	storageDir string
}

func NewUploadResumesUseCase(repo ResumeRepository, publisher EventPublisher, storageDir string) *UploadResumesUseCase {
	return &UploadResumesUseCase{repo: repo, publisher: publisher, storageDir: storageDir}
}

// Run writes every uploaded file to a new per-batch directory, creates a
// PENDING resume row for it, and publishes an ingest event so the worker
// picks it up asynchronously.
//
// Every filename is validated before anything touches disk/DB/Kafka, so an
// invalid filename anywhere in the batch rejects the whole batch atomically
// instead of leaving earlier files in the same request already committed
// with no way for the caller to find them. If a later, non-validation
// failure still occurs mid-batch (e.g. the database or Kafka becomes
// unreachable partway through), Run returns the partial response describing
// what did succeed alongside the error, rather than discarding it — the
// caller retains the batch ID and every resume ref already committed.
func (uc *UploadResumesUseCase) Run(ctx context.Context, files []UploadFile) (dto.UploadBatchResponse, error) {
	safeNames := make([]string, len(files))
	for i, f := range files {
		safeName, err := sanitizeFilename(f.Filename)
		if err != nil {
			return dto.UploadBatchResponse{}, fmt.Errorf("invalid filename %q: %w", f.Filename, err)
		}
		safeNames[i] = safeName
	}

	batchID := uuid.NewString()
	batchDir := filepath.Join(uc.storageDir, batchID)
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		return dto.UploadBatchResponse{}, fmt.Errorf("create batch dir: %w", err)
	}

	resp := dto.UploadBatchResponse{BatchID: batchID, Resumes: make([]dto.ResumeRef, 0, len(files))}

	for i, f := range files {
		safeName := safeNames[i]

		// Index-prefix the on-disk name so two files sharing a base name in
		// one batch (e.g. two candidates both uploading "resume.pdf") don't
		// overwrite each other; the resume row still stores the original
		// (sanitized) filename for display.
		storedName := fmt.Sprintf("%d_%s", i, safeName)
		path := filepath.Join(batchDir, storedName)
		if err := os.WriteFile(path, f.Content, 0o644); err != nil {
			return resp, fmt.Errorf("write file %s: %w", storedName, err)
		}

		resume := &domain.Resume{
			BatchID:  batchID,
			Filename: safeName,
			FilePath: path,
			Status:   domain.StatusPending,
		}
		if err := uc.repo.CreateResume(ctx, resume); err != nil {
			return resp, fmt.Errorf("create resume row for %s: %w", safeName, err)
		}

		if err := uc.publisher.PublishResumeIngest(ctx, resume.ID); err != nil {
			return resp, fmt.Errorf("publish ingest event for %s: %w", resume.ID, err)
		}

		resp.Resumes = append(resp.Resumes, dto.ResumeRef{ID: resume.ID, Filename: safeName})
	}

	return resp, nil
}

// sanitizeFilename strips any directory component from an
// attacker-controlled multipart filename (defending against path traversal
// like "../../etc/passwd") and rejects the degenerate cases that remain
// unsafe even after stripping.
func sanitizeFilename(name string) (string, error) {
	base := filepath.Base(filepath.Clean(name))
	if base == "" || base == "." || base == ".." {
		return "", fmt.Errorf("empty or unsafe filename")
	}
	return base, nil
}
