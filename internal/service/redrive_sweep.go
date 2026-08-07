package service

import (
	"context"
	"errors"
	"fmt"

	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
)

// RedriveSweepUseCase closes the crash-gap the 3-stage pipeline split would
// otherwise introduce: each stage does "DB write, then publish to the next
// topic," and if the process crashes between those two steps, the resume
// is durably parked at that stage with nothing left to ever republish it.
// Run periodically (see constants.SweepInterval) from cmd/app/worker.go, it
// claims resumes that have made no progress for constants.SweepStaleAfter
// and republishes each to the topic matching its current stage, or marks
// it FAILED once it has been redriven constants.MaxRedrives times without
// completing — a poison-pill cutoff so a genuinely broken PDF or a model
// that never returns valid JSON doesn't retry forever.
type RedriveSweepUseCase struct {
	repo                ResumeRepository
	extractPublisher    EventPublisher
	extractedPublisher  ExtractedPublisher
	classifiedPublisher ClassifiedPublisher
}

func NewRedriveSweepUseCase(
	repo ResumeRepository,
	extractPublisher EventPublisher,
	extractedPublisher ExtractedPublisher,
	classifiedPublisher ClassifiedPublisher,
) *RedriveSweepUseCase {
	return &RedriveSweepUseCase{
		repo:                repo,
		extractPublisher:    extractPublisher,
		extractedPublisher:  extractedPublisher,
		classifiedPublisher: classifiedPublisher,
	}
}

// Run claims one batch of stale resumes and redrives each. It returns a
// joined error describing every redrive that failed, if any, rather than
// stopping at the first — one resume's Kafka publish failing shouldn't
// block the rest of the batch from being redriven this sweep.
func (uc *RedriveSweepUseCase) Run(ctx context.Context) error {
	claimed, err := uc.repo.ClaimStaleForRedrive(ctx, constants.SweepStaleAfter, constants.MaxRedrives, constants.SweepBatchSize)
	if err != nil {
		return fmt.Errorf("claim stale resumes for redrive: %w", err)
	}

	var errs []error
	for _, resume := range claimed {
		if err := uc.redriveOne(ctx, resume); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (uc *RedriveSweepUseCase) redriveOne(ctx context.Context, resume domain.Resume) error {
	// ClaimStaleForRedrive already incremented RedriveCount as part of the
	// same atomic claim; RedriveCount > MaxRedrives here means this claim
	// is the one that pushed it over, not a claim that should have been
	// filtered out — see ClaimStaleForRedrive's doc comment in ports.go.
	//
	// This calls writeStatus directly rather than failResume: marking a
	// poison pill FAILED is this sweep step succeeding at its job, not a
	// processing failure to propagate. Only a genuine failure to record
	// that (the status write itself erroring) should surface as a sweep
	// error.
	if resume.RedriveCount > constants.MaxRedrives {
		msg := fmt.Sprintf("resume %s exceeded max redrive attempts (%d)", resume.ID, constants.MaxRedrives)
		return writeStatus(ctx, uc.repo, resume.ID, domain.StatusFailed, msg)
	}

	switch resume.Stage {
	case domain.StageExtract:
		return uc.extractPublisher.PublishResumeIngest(ctx, resume.ID)
	case domain.StageClassify:
		return uc.extractedPublisher.PublishResumeExtracted(ctx, resume.ID)
	case domain.StageEmbed:
		return uc.classifiedPublisher.PublishResumeClassified(ctx, resume.ID)
	default:
		return fmt.Errorf("resume %s has unrecognized stage %q, cannot redrive", resume.ID, resume.Stage)
	}
}
