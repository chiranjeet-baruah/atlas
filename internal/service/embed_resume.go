package service

import (
	"context"
	"fmt"
	"time"

	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
	"resumesearch/internal/utils"
)

// EmbedResumeUseCase is stage 3, the last stage of the pipeline: chunk the
// raw text stage 1 saved, embed each chunk, and mark the resume DONE. It
// consumes resume.fields.classified.
type EmbedResumeUseCase struct {
	repo  ResumeRepository
	model ModelClient
}

func NewEmbedResumeUseCase(repo ResumeRepository, model ModelClient) *EmbedResumeUseCase {
	return &EmbedResumeUseCase{repo: repo, model: model}
}

// Run processes one resume's embed stage. Safe to call more than once for
// the same resumeID — see ExtractResumeUseCase.Run's doc comment.
//
// Unlike the other two stages, embed has no AdvanceStage call after it:
// it's the last stage, so its terminal write is writeStatus(DONE) instead.
// That makes the isTerminal(resume.Status) guard below load-bearing in a
// way the other stages' guards aren't — see isTerminal's doc comment for
// why checking Stage instead would deadlock a redriven resume here.
// SaveChunks's ON CONFLICT (resume_id, chunk_index) upsert (see
// postgres.Repository.SaveChunks) is what makes a redundant re-run of this
// stage safe: redundant re-embedding is wasted work, not a correctness
// problem.
func (uc *EmbedResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, err := uc.repo.GetByID(ctx, resumeID)
	if err != nil {
		return fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if isTerminal(resume.Status) {
		return nil
	}

	if err := uc.repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}

	textChunks := utils.RecursiveSplit(resume.RawText, constants.ChunkSizeWords)

	// Embedding cost scales with chunk count, so the stage's budget is
	// computed from the actual count rather than a fixed constant (see
	// constants.EmbedStageTimeout's doc comment for the consumer-level
	// backstop this sits inside).
	embedBudget := time.Duration(len(textChunks))*constants.EmbedAttemptTimeout + constants.EmbedStageSlack
	stageCtx, cancel := context.WithTimeout(ctx, embedBudget)
	defer cancel()

	chunks := make([]domain.Chunk, 0, len(textChunks))
	for i, chunkText := range textChunks {
		embedding, err := uc.embedOne(stageCtx, chunkText)
		if err != nil {
			return failResume(ctx, uc.repo, resumeID, fmt.Errorf("embed chunk %d: %w", i, err))
		}
		chunks = append(chunks, domain.Chunk{ChunkIndex: i, ChunkText: chunkText, Embedding: embedding})
	}

	if err := uc.repo.SaveChunks(ctx, resumeID, chunks); err != nil {
		return failResume(ctx, uc.repo, resumeID, fmt.Errorf("save chunks: %w", err))
	}

	// SaveChunks already succeeded — a failure recording DONE from here is
	// the crash-gap the redrive sweeper exists to close, not a processing
	// failure. See ExtractResumeUseCase.Run's matching comment.
	if err := writeStatus(ctx, uc.repo, resumeID, domain.StatusDone, ""); err != nil {
		return fmt.Errorf("mark resume %s done: %w", resumeID, err)
	}
	return nil
}

// embedOne gives a single chunk its own sub-timeout within stageCtx, so one
// hung Embed call can't eat the budget reserved for later chunks — the same
// defense-in-depth pattern as modelclient.Client.Extract's per-attempt
// timeout.
func (uc *EmbedResumeUseCase) embedOne(stageCtx context.Context, chunkText string) ([]float32, error) {
	attemptCtx, cancel := context.WithTimeout(stageCtx, constants.EmbedAttemptTimeout)
	defer cancel()
	return uc.model.Embed(attemptCtx, chunkText)
}
