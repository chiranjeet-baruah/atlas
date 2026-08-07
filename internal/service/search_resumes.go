package service

import (
	"context"
	"fmt"

	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
	"resumesearch/internal/utils"
)

type SearchResumesUseCase struct {
	repo  ResumeRepository
	model ModelClient
}

func NewSearchResumesUseCase(repo ResumeRepository, model ModelClient) *SearchResumesUseCase {
	return &SearchResumesUseCase{repo: repo, model: model}
}

func (uc *SearchResumesUseCase) Run(ctx context.Context, req dto.SearchRequest) (dto.SearchResponse, error) {
	queryVec, err := uc.model.Embed(ctx, req.Query)
	if err != nil {
		return dto.SearchResponse{}, fmt.Errorf("embed query: %w", err)
	}

	filters := domain.SearchFilters{
		RequiredSkills: utils.NormalizeSkills(req.RequiredSkills),
		MinYears:       req.MinYears,
		Location:       req.Location,
	}

	results, err := uc.repo.Search(ctx, queryVec, filters, constants.SearchResultLimit)
	if err != nil {
		return dto.SearchResponse{}, fmt.Errorf("search: %w", err)
	}

	return dto.SearchResponse{Results: dto.FromSearchResults(results)}, nil
}
