package service

import (
	"context"
	"fmt"

	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
)

type GetStatusUseCase struct {
	repo ResumeRepository
}

func NewGetStatusUseCase(repo ResumeRepository) *GetStatusUseCase {
	return &GetStatusUseCase{repo: repo}
}

// ByID wraps repository errors with %w so domain.ErrNotFound survives to
// the HTTP driver, which is what decides 404 vs 500 — this use case has no
// business making that call.
func (uc *GetStatusUseCase) ByID(ctx context.Context, id string) (dto.StatusResponse, error) {
	resume, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return dto.StatusResponse{}, fmt.Errorf("get resume %s: %w", id, err)
	}
	return dto.FromResume(resume), nil
}

// FileByID returns the on-disk path and original filename for a resume's
// uploaded file, so an HTTP handler can stream the bytes back. Same
// domain.ErrNotFound-preserving contract as ByID.
func (uc *GetStatusUseCase) FileByID(ctx context.Context, id string) (dto.ResumeFileInfo, error) {
	resume, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return dto.ResumeFileInfo{}, fmt.Errorf("get resume %s: %w", id, err)
	}
	return dto.FromResumeFile(resume), nil
}

// ByBatchID treats a batch with no resumes as domain.ErrNotFound, not an
// empty result: every batch is created by UploadResumes with at least one
// file, so zero rows means this batch ID was never created, matching ByID's
// 404 semantics for an unknown resume rather than silently returning an
// empty list.
func (uc *GetStatusUseCase) ByBatchID(ctx context.Context, batchID string) (dto.BatchStatusResponse, error) {
	resumes, err := uc.repo.GetByBatchID(ctx, batchID)
	if err != nil {
		return dto.BatchStatusResponse{}, fmt.Errorf("get batch %s: %w", batchID, err)
	}
	if len(resumes) == 0 {
		return dto.BatchStatusResponse{}, fmt.Errorf("get batch %s: %w", batchID, domain.ErrNotFound)
	}
	return dto.BatchStatusResponse{BatchID: batchID, Resumes: dto.FromResumes(resumes)}, nil
}

// ListBatches returns every batch's aggregate status counts. Unlike
// ByBatchID, an empty result is not domain.ErrNotFound: zero batches means
// none have been created yet, not that a specific requested ID is missing.
func (uc *GetStatusUseCase) ListBatches(ctx context.Context) ([]dto.BatchSummary, error) {
	summaries, err := uc.repo.ListBatches(ctx)
	if err != nil {
		return nil, fmt.Errorf("list batches: %w", err)
	}
	return dto.FromBatchSummaries(summaries), nil
}
