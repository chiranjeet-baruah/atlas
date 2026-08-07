package service_test

import (
	"context"
	"time"

	"resumesearch/internal/domain"
)

// fakeRepo is the single configurable ResumeRepository test double shared
// by every use case test in this package. Each method delegates to an
// optional function field, so a given test only wires the behavior it
// actually exercises; every call is also recorded for assertions.
type fakeRepo struct {
	CreateResumeFn         func(ctx context.Context, r *domain.Resume) error
	UpdateStatusFn         func(ctx context.Context, id string, status domain.Status, errMsg string) error
	AdvanceStageFn         func(ctx context.Context, id string, stage string) error
	SaveRawTextFn          func(ctx context.Context, id, rawText string) error
	SaveExtractedFieldsFn  func(ctx context.Context, id string, fields domain.ExtractedFields) error
	SaveChunksFn           func(ctx context.Context, resumeID string, chunks []domain.Chunk) error
	GetByIDFn              func(ctx context.Context, id string) (domain.Resume, error)
	GetByBatchIDFn         func(ctx context.Context, batchID string) ([]domain.Resume, error)
	SearchFn               func(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error)
	ClaimStaleForRedriveFn func(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error)

	CreatedResumes   []domain.Resume
	StatusCalls      []domain.Status
	StageCalls       []string
	SavedRawText     string
	SavedFields      domain.ExtractedFields
	SavedChunks      []domain.Chunk
	SearchGotVec     []float32
	SearchGotFilters domain.SearchFilters
}

func (f *fakeRepo) CreateResume(ctx context.Context, r *domain.Resume) error {
	if f.CreateResumeFn != nil {
		if err := f.CreateResumeFn(ctx, r); err != nil {
			return err
		}
	}
	f.CreatedResumes = append(f.CreatedResumes, *r)
	return nil
}

func (f *fakeRepo) UpdateStatus(ctx context.Context, id string, status domain.Status, errMsg string) error {
	f.StatusCalls = append(f.StatusCalls, status)
	if f.UpdateStatusFn != nil {
		return f.UpdateStatusFn(ctx, id, status, errMsg)
	}
	return nil
}

func (f *fakeRepo) AdvanceStage(ctx context.Context, id string, stage string) error {
	f.StageCalls = append(f.StageCalls, stage)
	if f.AdvanceStageFn != nil {
		return f.AdvanceStageFn(ctx, id, stage)
	}
	return nil
}

func (f *fakeRepo) SaveRawText(ctx context.Context, id, rawText string) error {
	f.SavedRawText = rawText
	if f.SaveRawTextFn != nil {
		return f.SaveRawTextFn(ctx, id, rawText)
	}
	return nil
}

func (f *fakeRepo) SaveExtractedFields(ctx context.Context, id string, fields domain.ExtractedFields) error {
	f.SavedFields = fields
	if f.SaveExtractedFieldsFn != nil {
		return f.SaveExtractedFieldsFn(ctx, id, fields)
	}
	return nil
}

func (f *fakeRepo) SaveChunks(ctx context.Context, resumeID string, chunks []domain.Chunk) error {
	f.SavedChunks = chunks
	if f.SaveChunksFn != nil {
		return f.SaveChunksFn(ctx, resumeID, chunks)
	}
	return nil
}

func (f *fakeRepo) GetByID(ctx context.Context, id string) (domain.Resume, error) {
	if f.GetByIDFn != nil {
		return f.GetByIDFn(ctx, id)
	}
	return domain.Resume{}, nil
}

func (f *fakeRepo) GetByBatchID(ctx context.Context, batchID string) ([]domain.Resume, error) {
	if f.GetByBatchIDFn != nil {
		return f.GetByBatchIDFn(ctx, batchID)
	}
	return nil, nil
}

func (f *fakeRepo) Search(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error) {
	f.SearchGotVec = queryVec
	f.SearchGotFilters = filters
	if f.SearchFn != nil {
		return f.SearchFn(ctx, queryVec, filters, limit)
	}
	return nil, nil
}

func (f *fakeRepo) ClaimStaleForRedrive(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error) {
	if f.ClaimStaleForRedriveFn != nil {
		return f.ClaimStaleForRedriveFn(ctx, staleAfter, maxRedrives, limit)
	}
	return nil, nil
}

// fakeModel is the single configurable ModelClient test double shared by
// every use case test in this package.
type fakeModel struct {
	ExtractFn func(ctx context.Context, text string) (domain.ExtractedFields, error)
	EmbedFn   func(ctx context.Context, text string) ([]float32, error)

	ExtractCalled bool
}

func (f *fakeModel) Extract(ctx context.Context, text string) (domain.ExtractedFields, error) {
	f.ExtractCalled = true
	if f.ExtractFn != nil {
		return f.ExtractFn(ctx, text)
	}
	return domain.ExtractedFields{Skills: []string{"Go", "PostgreSQL"}, YearsExperience: 5, Location: "Remote"}, nil
}

func (f *fakeModel) Embed(ctx context.Context, text string) ([]float32, error) {
	if f.EmbedFn != nil {
		return f.EmbedFn(ctx, text)
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

// fakeExtractor is the single configurable TextExtractor test double
// shared by every use case test in this package.
type fakeExtractor struct {
	ExtractTextFn func(ctx context.Context, path string) (string, error)
}

func (f *fakeExtractor) ExtractText(ctx context.Context, path string) (string, error) {
	if f.ExtractTextFn != nil {
		return f.ExtractTextFn(ctx, path)
	}
	return "", nil
}

// fakePublisher is the single configurable EventPublisher test double
// shared by every use case test in this package.
type fakePublisher struct {
	PublishResumeIngestFn func(ctx context.Context, resumeID string) error
	Published             []string
}

func (f *fakePublisher) PublishResumeIngest(ctx context.Context, resumeID string) error {
	f.Published = append(f.Published, resumeID)
	if f.PublishResumeIngestFn != nil {
		return f.PublishResumeIngestFn(ctx, resumeID)
	}
	return nil
}

// fakeExtractedPublisher is the single configurable ExtractedPublisher test
// double shared by every use case test in this package.
type fakeExtractedPublisher struct {
	PublishResumeExtractedFn func(ctx context.Context, resumeID string) error
	Published                []string
}

func (f *fakeExtractedPublisher) PublishResumeExtracted(ctx context.Context, resumeID string) error {
	f.Published = append(f.Published, resumeID)
	if f.PublishResumeExtractedFn != nil {
		return f.PublishResumeExtractedFn(ctx, resumeID)
	}
	return nil
}

// fakeClassifiedPublisher is the single configurable ClassifiedPublisher
// test double shared by every use case test in this package.
type fakeClassifiedPublisher struct {
	PublishResumeClassifiedFn func(ctx context.Context, resumeID string) error
	Published                 []string
}

func (f *fakeClassifiedPublisher) PublishResumeClassified(ctx context.Context, resumeID string) error {
	f.Published = append(f.Published, resumeID)
	if f.PublishResumeClassifiedFn != nil {
		return f.PublishResumeClassifiedFn(ctx, resumeID)
	}
	return nil
}
