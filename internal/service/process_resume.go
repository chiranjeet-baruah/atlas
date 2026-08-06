package service

import (
	"context"
	"fmt"

	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
	"resumesearch/internal/utils"
)

// ProcessResumeUseCase orchestrates the full per-resume pipeline: extract
// text, LLM-extract structured fields, chunk, embed each chunk, persist.
// This is what the Kafka worker invokes per consumed message.
type ProcessResumeUseCase struct {
	repo      ResumeRepository
	model     ModelClient
	extractor TextExtractor
}

func NewProcessResumeUseCase(repo ResumeRepository, model ModelClient, extractor TextExtractor) *ProcessResumeUseCase {
	return &ProcessResumeUseCase{repo: repo, model: model, extractor: extractor}
}

// Run processes one resume. It is safe to call more than once for the same
// resumeID — Kafka redelivers on redelivery/at-least-once semantics, and a
// resume already marked DONE is skipped rather than reprocessed.
func (uc *ProcessResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, err := uc.repo.GetByID(ctx, resumeID)
	if err != nil {
		return fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if resume.Status == domain.StatusDone {
		return nil
	}

	if err := uc.repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}

	if procErr := uc.process(ctx, resume); procErr != nil {
		if statusErr := uc.repo.UpdateStatus(ctx, resumeID, domain.StatusFailed, procErr.Error()); statusErr != nil {
			return fmt.Errorf("%w (and failed to record failure status: %v)", procErr, statusErr)
		}
		return procErr
	}

	return uc.repo.UpdateStatus(ctx, resumeID, domain.StatusDone, "")
}

func (uc *ProcessResumeUseCase) process(ctx context.Context, resume domain.Resume) error {
	rawText, err := uc.extractor.ExtractText(ctx, resume.FilePath)
	if err != nil {
		return fmt.Errorf("extract text: %w", err)
	}

	fields, err := uc.model.Extract(ctx, rawText)
	if err != nil {
		return fmt.Errorf("llm extract: %w", err)
	}
	fields.Skills = utils.NormalizeSkills(fields.Skills)

	if err := uc.repo.SaveExtraction(ctx, resume.ID, rawText, fields); err != nil {
		return fmt.Errorf("save extraction: %w", err)
	}

	textChunks := utils.RecursiveSplit(rawText, constants.ChunkSizeTokens)
	chunks := make([]domain.Chunk, 0, len(textChunks))
	for i, chunkText := range textChunks {
		embedding, err := uc.model.Embed(ctx, chunkText)
		if err != nil {
			return fmt.Errorf("embed chunk %d: %w", i, err)
		}
		chunks = append(chunks, domain.Chunk{ChunkIndex: i, ChunkText: chunkText, Embedding: embedding})
	}

	return uc.repo.SaveChunks(ctx, resume.ID, chunks)
}
