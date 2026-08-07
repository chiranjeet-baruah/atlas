package constants

import "time"

const (
	// KafkaTopic is the topic the extract stage's workers consume from.
	// Unchanged from before the pipeline was split into stages, so existing
	// committed offsets and any in-flight messages are unaffected.
	KafkaTopic = "resume.ingest.requested"

	// ConsumerGroup lets multiple extract-stage worker replicas share
	// partitions safely.
	ConsumerGroup = "resume-workers"

	// TopicResumeExtracted and GroupResumeClassify are the classify
	// stage's topic/consumer-group: published once the extract stage's
	// AdvanceStage write succeeds.
	TopicResumeExtracted = "resume.text.extracted"
	GroupResumeClassify  = "resume-classify-workers"

	// TopicResumeClassified and GroupResumeEmbed are the embed stage's
	// topic/consumer-group: published once the classify stage's
	// AdvanceStage write succeeds.
	TopicResumeClassified = "resume.fields.classified"
	GroupResumeEmbed      = "resume-embed-workers"

	// ExtractStageTimeout bounds the extract stage's consumer: pdftotext's
	// fast path is near-instant, and the OCR fallback's measured worst case
	// is ~6.5s (5 pages × ~1.3s/page, see decisions.md) — 30s leaves ample
	// margin without letting a wedged pdftoppm/tesseract subprocess block
	// the consumer forever.
	ExtractStageTimeout = 30 * time.Second

	// ClassifyStageTimeout bounds the classify stage's consumer: 3
	// independent LLM attempts (see MaxExtractionRetries, LLMAttemptTimeout)
	// plus slack for the surrounding save/publish work.
	ClassifyStageTimeout = time.Duration(MaxExtractionRetries)*LLMAttemptTimeout + 30*time.Second

	// StatusWriteTimeout bounds writeStatus's own UpdateStatus call. It is
	// deliberately short and independent of the caller's (possibly already
	// expired) context — see internal/service/status_write.go — because a
	// status write is a single fast Postgres UPDATE and never legitimately
	// needs longer.
	StatusWriteTimeout = 5 * time.Second

	// LLMAttemptTimeout bounds a single LLM extraction attempt. Measured,
	// not guessed: three real calls to docker.io/ai/llama3.2 with a real
	// (short) resume took 24.97s/1.53s/1.86s — the first call pays for
	// loading the model into Docker Model Runner, subsequent calls hit the
	// warm model and finish in ~2s. The cold-start cost recurs whenever
	// the model gets evicted for idleness or the worker restarts, so the
	// budget is sized off that worst case, not the warm-path average: 60s
	// leaves ~35s of margin over the observed cold start on a short
	// resume. Re-measure if production resumes run substantially longer
	// than that sample, or if the model is changed again.
	LLMAttemptTimeout = 60 * time.Second

	// EmbedAttemptTimeout bounds a single chunk's Embed call within the
	// embed stage. EmbedStageSlack is added on top of
	// len(chunks)*EmbedAttemptTimeout to size that stage's overall budget,
	// since embedding cost scales with chunk count, not a fixed constant.
	EmbedAttemptTimeout = 15 * time.Second
	EmbedStageSlack     = 15 * time.Second

	// MaxEmbedChunks and EmbedStageTimeout exist only to give the embed
	// stage's Kafka consumer a fixed backstop handlerTimeout — every
	// consumer needs one (see kafka.NewConsumer), but the embed stage's
	// real per-message budget is computed at runtime from the actual chunk
	// count (see embedBudget in embed_resume.go), which is normally much
	// smaller than this ceiling. MaxEmbedChunks is a generous upper bound
	// on how many chunks a single resume could realistically produce; this
	// ceiling only matters for a pathologically long or malformed document.
	MaxEmbedChunks    = 50
	EmbedStageTimeout = time.Duration(MaxEmbedChunks)*EmbedAttemptTimeout + EmbedStageSlack

	// SweepInterval is how often the redrive sweeper (internal/service/redrive_sweep.go)
	// runs. SweepStaleAfter is how long a resume must sit with no progress
	// (updated_at unchanged) before the sweeper claims it as stuck.
	//
	// It is defined relative to EmbedStageTimeout — not a standalone
	// constant — because EmbedStageTimeout is the largest of the 3 stage
	// timeouts AND the embed stage never advances updated_at between its
	// initial UpdateStatus(PROCESSING) and the final SaveChunks/writeStatus
	// (unlike extract/classify, it has no AdvanceStage call mid-stage). A
	// resume legitimately embedding a large chunk count can sit with a
	// stale updated_at for up to EmbedStageTimeout; if SweepStaleAfter were
	// shorter than that (it was previously a flat 5m, less than
	// EmbedStageTimeout's 12m45s), the sweeper would claim and republish a
	// resume that is still being correctly processed, double-embedding it
	// and burning its redrive_count for no reason. Tying it to
	// EmbedStageTimeout with a margin means a future change to
	// MaxEmbedChunks/EmbedAttemptTimeout can't silently reintroduce this.
	//
	// MaxRedrives caps how many times a single resume can be redriven
	// before the sweeper gives up and marks it FAILED, so a genuinely
	// broken PDF or a model that never returns valid JSON doesn't retry
	// forever. SweepBatchSize caps rows claimed per sweep tick.
	SweepInterval   = 2 * time.Minute
	SweepStaleAfter = EmbedStageTimeout + 2*time.Minute
	MaxRedrives     = 5
	SweepBatchSize  = 50

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

	// ExtractionRetryBackoff is a short pause between LLM extraction
	// attempts. Without it, a retry against a model that just failed (e.g.
	// mid-restart, or momentarily overloaded) fires immediately with zero
	// delay.
	ExtractionRetryBackoff = 500 * time.Millisecond

	// EmbeddingDimension is the output size of ai/nomic-embed-text-v1.5.
	// Must match the `resume_chunks.embedding VECTOR(N)` column in
	// migrations/0001_init.up.sql exactly — verified against a live
	// /engines/v1/embeddings call, not assumed.
	EmbeddingDimension = 768

	// MinExtractedTextChars is the minimum trimmed-text length below which
	// an extraction is treated as "no usable text" and triggers the OCR
	// fallback (or, if OCR also falls short, ErrNoExtractableText). Scanned
	// pages collapse to "" here because pdftotext's form-feed page marker
	// is whitespace under strings.TrimSpace. Confirmed against real OCR
	// output in the design spike — see decisions.md.
	MinExtractedTextChars = 100

	// MaxOCRPages bounds how many pages the OCR fallback rasterizes and
	// reads. OCR costs roughly a second per page (measured: ~1.3s/page
	// combined pdftoppm+tesseract), so an unbounded page count risks
	// blowing ResumeProcessingTimeout on a long scanned document. Resumes
	// are essentially never longer than this in practice.
	MaxOCRPages = 5

	// OCRRasterDPI is the resolution pdftoppm rasterizes pages at before
	// handing them to tesseract. Higher DPI improves OCR accuracy at the
	// cost of time; 200 produced clean, accurate OCR text against a real
	// scanned resume in the design spike — see decisions.md.
	OCRRasterDPI = 200
)
