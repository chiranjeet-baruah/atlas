package service

import (
	"context"
	"fmt"

	"resumesearch/internal/domain"
)

// ExtractResumeUseCase is stage 1 of the pipeline: pull raw text out of the
// uploaded PDF and hand off to stage 2 (classify). It consumes
// resume.ingest.requested — the topic UploadResumesUseCase already
// publishes to — so its topic/consumer-group names are unchanged from
// before the pipeline was split into stages.
type ExtractResumeUseCase struct {
	repo      ResumeRepository
	extractor TextExtractor
	publisher ExtractedPublisher
}

func NewExtractResumeUseCase(repo ResumeRepository, extractor TextExtractor, publisher ExtractedPublisher) *ExtractResumeUseCase {
	return &ExtractResumeUseCase{repo: repo, extractor: extractor, publisher: publisher}
}

// Run processes one resume's extract stage. It is safe to call more than
// once for the same resumeID — Kafka redelivers under at-least-once
// semantics, and a resume already in a terminal state (DONE or FAILED) is
// skipped rather than reprocessed.
func (uc *ExtractResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, err := uc.repo.GetByID(ctx, resumeID)
	if err != nil {
		return fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if resume.Status.IsTerminal() {
		return nil
	}

	if err := uc.repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}

	rawText, err := uc.extractor.ExtractText(ctx, resume.FilePath)
	if err != nil {
		return failResume(ctx, uc.repo, resumeID, fmt.Errorf("extract text: %w", err))
	}

	if err := uc.repo.SaveRawText(ctx, resumeID, rawText); err != nil {
		return failResume(ctx, uc.repo, resumeID, fmt.Errorf("save raw text: %w", err))
	}

	if err := uc.repo.AdvanceStage(ctx, resumeID, domain.StageClassify); err != nil {
		return failResume(ctx, uc.repo, resumeID, fmt.Errorf("advance stage: %w", err))
	}

	// The stage's own work (raw text saved, stage advanced) is already
	// durably recorded at this point. A publish failure here is the
	// crash-gap the redrive sweeper exists to close, not a processing
	// failure — leave the resume at status=PROCESSING/stage=CLASSIFY for
	// the sweeper to redrive rather than marking it FAILED over work that
	// actually succeeded.
	if err := uc.publisher.PublishResumeExtracted(ctx, resumeID); err != nil {
		return fmt.Errorf("publish resume extracted for %s: %w", resumeID, err)
	}
	return nil
}
