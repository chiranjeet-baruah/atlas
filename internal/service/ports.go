package service

import (
	"context"

	"resumesearch/internal/domain"
)

// ResumeRepository is the outbound port to persistence. Implemented by
// internal/adapter/driven/postgres. Lookup methods return domain.ErrNotFound
// (wrapped) when no row matches, so callers can distinguish "missing" from
// "the database is down".
type ResumeRepository interface {
	CreateResume(ctx context.Context, r *domain.Resume) error
	UpdateStatus(ctx context.Context, id string, status domain.Status, errMsg string) error
	SaveExtraction(ctx context.Context, id string, rawText string, fields domain.ExtractedFields) error
	SaveChunks(ctx context.Context, resumeID string, chunks []domain.Chunk) error
	GetByID(ctx context.Context, id string) (domain.Resume, error)
	GetByBatchID(ctx context.Context, batchID string) ([]domain.Resume, error)
	Search(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error)
}

// ModelClient is the outbound port to the LLM + embedding model.
// Implemented by internal/adapter/driven/modelclient, a single
// OpenAI-compatible HTTP client parameterized by base URL + model name —
// never one adapter per provider.
type ModelClient interface {
	Extract(ctx context.Context, text string) (domain.ExtractedFields, error)
	Embed(ctx context.Context, text string) ([]float32, error)
}

// TextExtractor is the outbound port to PDF text extraction.
// Implemented by internal/adapter/driven/pdf.
type TextExtractor interface {
	ExtractText(ctx context.Context, path string) (string, error)
}

// EventPublisher is the outbound port to Kafka production.
// Implemented by internal/adapter/driven/kafka.
type EventPublisher interface {
	PublishResumeIngest(ctx context.Context, resumeID string) error
}

// EventConsumer is the inbound port that drives the worker: it consumes
// resume-ingest events and invokes handler for each one. Implemented by
// internal/adapter/driver/kafka.
type EventConsumer interface {
	Consume(ctx context.Context, handler func(ctx context.Context, resumeID string) error) error
}
