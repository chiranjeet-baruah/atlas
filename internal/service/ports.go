package service

import (
	"context"
	"time"

	"resumesearch/internal/domain"
)

// ResumeRepository is the outbound port to persistence. Implemented by
// internal/adapter/driven/postgres. Lookup methods return domain.ErrNotFound
// (wrapped) when no row matches, so callers can distinguish "missing" from
// "the database is down".
type ResumeRepository interface {
	CreateResume(ctx context.Context, r *domain.Resume) error
	UpdateStatus(ctx context.Context, id string, status domain.Status, errMsg string) error

	// AdvanceStage bumps a resume's Stage (and updated_at) once a pipeline
	// stage's own DB write has succeeded, before that stage publishes to
	// the next topic. If the process crashes between the write and the
	// publish, the row is left at the new stage with a stale updated_at —
	// exactly what the redrive sweeper looks for.
	AdvanceStage(ctx context.Context, id string, stage string) error

	// SaveRawText and SaveExtractedFields replace the old single
	// SaveExtraction: the extract stage only has raw text to save, the
	// classify stage only has structured fields, and neither should be able
	// to accidentally clobber the other's column with a zero value.
	SaveRawText(ctx context.Context, id string, rawText string) error
	SaveExtractedFields(ctx context.Context, id string, fields domain.ExtractedFields) error

	SaveChunks(ctx context.Context, resumeID string, chunks []domain.Chunk) error
	GetByID(ctx context.Context, id string) (domain.Resume, error)
	GetByBatchID(ctx context.Context, batchID string) ([]domain.Resume, error)

	// ListBatches returns every batch's aggregate status counts, newest
	// first, capped at constants.ProcessingBatchListLimit. Unlike
	// GetByBatchID, an empty result is not an error: it means no batches
	// exist yet, not that one specific ID was never created.
	ListBatches(ctx context.Context) ([]domain.BatchSummary, error)

	Search(ctx context.Context, queryVec []float32, filters domain.SearchFilters, limit int) ([]domain.SearchResult, error)

	// ClaimStaleForRedrive atomically claims up to limit resumes that have
	// been sitting at PENDING/PROCESSING with no progress (updated_at
	// unchanged) for longer than staleAfter, and whose redrive_count is
	// still at or under maxRedrives. "Atomically" matters because multiple
	// worker replicas each run a sweeper: a plain SELECT ... FOR UPDATE
	// SKIP LOCKED doesn't prevent double-claiming here, since the lock
	// releases before the Kafka publish happens outside the transaction.
	// The returned rows' RedriveCount already reflects this claim's
	// increment, so a caller sees RedriveCount > maxRedrives exactly when
	// this claim is the one that pushed it over.
	ClaimStaleForRedrive(ctx context.Context, staleAfter time.Duration, maxRedrives, limit int) ([]domain.Resume, error)
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

// EventPublisher is the outbound port to Kafka production for stage 1
// (extract). Implemented by internal/adapter/driven/kafka.
type EventPublisher interface {
	PublishResumeIngest(ctx context.Context, resumeID string) error
}

// ExtractedPublisher is the outbound port to Kafka production for stage 2
// (classify) — published once the extract stage's AdvanceStage write
// succeeds. Implemented by internal/adapter/driven/kafka.
type ExtractedPublisher interface {
	PublishResumeExtracted(ctx context.Context, resumeID string) error
}

// ClassifiedPublisher is the outbound port to Kafka production for stage 3
// (embed) — published once the classify stage's AdvanceStage write
// succeeds. Implemented by internal/adapter/driven/kafka.
type ClassifiedPublisher interface {
	PublishResumeClassified(ctx context.Context, resumeID string) error
}

// EventConsumer is the inbound port that drives the worker: it consumes
// resume-ingest events and invokes handler for each one. Implemented by
// internal/adapter/driver/kafka.
type EventConsumer interface {
	Consume(ctx context.Context, handler func(ctx context.Context, resumeID string) error) error
}
