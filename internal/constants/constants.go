package constants

import "time"

const (
	// KafkaTopic is the topic workers consume resume-ingest events from.
	KafkaTopic = "resume.ingest.requested"

	// ConsumerGroup lets multiple worker replicas share partitions safely.
	ConsumerGroup = "resume-workers"

	// ResumeProcessingTimeout bounds how long the worker will wait on a
	// single resume's PDF extraction + LLM + embedding calls before giving
	// up. Without this, a single hung Docker Model Runner request or a
	// wedged pdftotext subprocess blocks the consumer's single processing
	// goroutine forever — no more resumes are ever processed until the
	// process is manually restarted.
	ResumeProcessingTimeout = 2 * time.Minute

	// MaxUploadBytes bounds one batch-upload request's total body size, so
	// a single request can't exhaust memory/disk on this single-process
	// server. Generous for the realistic resume-PDF sizes this project
	// targets.
	MaxUploadBytes = 100 << 20 // 100 MiB

	// MaxUploadFiles bounds the number of files accepted in one batch
	// upload request.
	MaxUploadFiles = 200

	// MaxSearchBodyBytes bounds a single /search request body — a search
	// request is a short query plus a few filter fields and should never
	// legitimately approach this size.
	MaxSearchBodyBytes = 64 << 10 // 64 KiB

	// ChunkSizeTokens: recursive fixed-size chunking, ~512 tokens, no overlap.
	// Validated against 2026 RAG chunking benchmarks (recursive splitting
	// outperformed semantic/section-based chunking on retrieval accuracy;
	// overlap showed no measurable benefit in recent studies) — see decisions.md.
	ChunkSizeTokens = 512

	// SearchResultLimit is the default number of ranked results returned.
	SearchResultLimit = 20

	// MaxExtractionRetries bounds retries when the LLM returns invalid JSON
	// for structured field extraction (small/local models are the highest-risk
	// component for schema adherence).
	MaxExtractionRetries = 3

	// EmbeddingDimension is the output size of ai/nomic-embed-text-v1.5.
	// Must match the `resume_chunks.embedding VECTOR(N)` column in
	// migrations/0001_init.up.sql exactly — verified against a live
	// /engines/v1/embeddings call, not assumed.
	EmbeddingDimension = 768
)
