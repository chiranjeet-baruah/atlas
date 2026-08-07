package service

import (
	"context"
	"fmt"

	"resumesearch/internal/domain"
	"resumesearch/internal/utils"
)

// ClassifyResumeUseCase is stage 2 of the pipeline: LLM-extract structured
// fields (skills/years_experience/location) from the raw text stage 1
// saved, then hand off to stage 3 (embed). It consumes
// resume.text.extracted.
type ClassifyResumeUseCase struct {
	repo      ResumeRepository
	model     ModelClient
	publisher ClassifiedPublisher
}

func NewClassifyResumeUseCase(repo ResumeRepository, model ModelClient, publisher ClassifiedPublisher) *ClassifyResumeUseCase {
	return &ClassifyResumeUseCase{repo: repo, model: model, publisher: publisher}
}

// Run processes one resume's classify stage. Safe to call more than once
// for the same resumeID — see ExtractResumeUseCase.Run's doc comment.
func (uc *ClassifyResumeUseCase) Run(ctx context.Context, resumeID string) error {
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

	fields, err := uc.model.Extract(ctx, resume.RawText)
	if err != nil {
		return failResume(ctx, uc.repo, resumeID, fmt.Errorf("llm extract: %w", err))
	}
	fields.Skills = utils.NormalizeSkills(fields.Skills)

	if err := uc.repo.SaveExtractedFields(ctx, resumeID, fields); err != nil {
		return failResume(ctx, uc.repo, resumeID, fmt.Errorf("save extracted fields: %w", err))
	}

	if err := uc.repo.AdvanceStage(ctx, resumeID, domain.StageEmbed); err != nil {
		return failResume(ctx, uc.repo, resumeID, fmt.Errorf("advance stage: %w", err))
	}

	// See ExtractResumeUseCase.Run's matching comment: a publish failure
	// here leaves status=PROCESSING/stage=EMBED for the sweeper, rather
	// than marking FAILED over classification work that already succeeded.
	if err := uc.publisher.PublishResumeClassified(ctx, resumeID); err != nil {
		return fmt.Errorf("publish resume classified for %s: %w", resumeID, err)
	}
	return nil
}
