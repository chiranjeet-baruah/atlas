package service

import (
	"context"
	"errors"
	"fmt"

	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
)

// writeStatus records a resume's status without inheriting the caller's
// context lifetime. Every stage use case calls this instead of
// repo.UpdateStatus(ctx, ...) directly for its FAILED and terminal-success
// writes.
//
// Without this, a status write made after a processing timeout reuses the
// same ctx that just expired — so the write fails too, and the resume is
// left permanently at StatusProcessing with nothing recording why. This was
// confirmed against the live DB: 11 rows wedged at PROCESSING after an LLM
// timeout, because the FAILED write that should have followed shared the
// already-expired deadline. context.WithoutCancel strips the parent's
// expired deadline/cancellation while keeping its values (e.g. trace IDs);
// WithTimeout then gives the write its own short, fresh budget.
func writeStatus(ctx context.Context, repo ResumeRepository, id string, status domain.Status, errMsg string) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.StatusWriteTimeout)
	defer cancel()
	return repo.UpdateStatus(wctx, id, status, errMsg)
}

// beginStage loads resumeID and marks it Processing, unless it's already
// in a terminal state — in which case the caller should return nil and
// skip its stage-specific work, treating this as a no-op success. Every
// pipeline stage (extract/classify/embed) starts its Run this way; this
// exists so that identical preamble isn't hand-rolled three times with the
// same error-wrap strings and the same terminal-skip logic.
func beginStage(ctx context.Context, repo ResumeRepository, resumeID string) (domain.Resume, bool, error) {
	resume, err := repo.GetByID(ctx, resumeID)
	if err != nil {
		return domain.Resume{}, false, fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if resume.Status.IsTerminal() {
		return resume, false, nil
	}
	if err := repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return domain.Resume{}, false, fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}
	return resume, true, nil
}

// failResume marks a resume FAILED via writeStatus and returns procErr,
// the error that triggered the failure. If the FAILED write itself fails
// (writeStatus's whole reason to exist doesn't make that impossible, just
// far less likely — e.g. the database is genuinely down), the returned
// error wraps both procErr and domain.ErrStatusNotRecorded via
// errors.Join, so the Kafka consumer can tell "failure durably recorded,
// safe to commit the offset" apart from "failure not recorded, redeliver"
// using errors.Is, instead of committing an offset whose failure was never
// actually written down.
func failResume(ctx context.Context, repo ResumeRepository, id string, procErr error) error {
	if statusErr := writeStatus(ctx, repo, id, domain.StatusFailed, procErr.Error()); statusErr != nil {
		return fmt.Errorf("%w (and failed to record failure status: %w)", procErr, errors.Join(domain.ErrStatusNotRecorded, statusErr))
	}
	return procErr
}
