# Decisions

A running log of the calls made while building this, and why. Kept current — superseded entries get merged into whatever replaced them, not left standing alongside it.

## Why Go, not Python

Python has the obvious ecosystem edge for PDF/NLP, and a Go+Python split (API in Go, ingestion in Python) was tempting. Went all-Go instead: one toolchain, and the concurrency model fits a worker pool naturally. Tradeoff: `pdftotext` (shelled out) instead of Python's PDF libraries.

## Model backends: Docker Model Runner for embeddings, a hosted API for chat

Wanted `docker compose up` and done — no separate model server, no API keys, no cost — for as much of this as possible. Docker Model Runner's `models:` block in Compose gives exactly that, and it's what still serves embeddings (`ai/nomic-embed-text-v1.5`). One thing worth knowing if this ever gets re-pointed: DMR's injected URL already has a trailing slash and a `/v1` in it (`http://model-runner.docker.internal/v1/`) — `modelclient.New` trims the trailing slash so this doesn't bite twice.

Chat/extraction moved off DMR to a hosted OpenAI-compatible API (Groq, `llama-3.1-8b-instant`) — small local models were shakier at sticking to a JSON schema, and DMR's cold-start (~25s on a weak VPS) needed a 120s timeout plus a proactive warm-up ticker just to stay reliable. Both backends speak the same OpenAI-compatible chat/embeddings shape (DMR, Ollama, hosted APIs all do), so this stayed a config/auth change to one `modelclient.Client` — no per-provider adapters, no abstraction layer that would've existed on paper only.

A billed API isn't free like DMR was, so three things that didn't matter before got fixed as part of the move, not left as follow-ups: `WarmUp` no longer touches chat at all (nothing to warm on a hosted API); `Extract` now caps prompt size (`MaxExtractionTextChars`) and completion length (`MaxExtractionCompletionTokens`) so a pathological resume or a rambling completion can't produce an unbounded bill; and the retry loop honors a 429's `Retry-After` instead of hammering on a fixed backoff (DMR never rate-limited, a hosted API will). Per-call token usage is logged via `slog` so spend is visible from day one.

`LLMAttemptTimeout` (120s) is still sized for DMR's old cold-start and hasn't been re-measured against the hosted API's actual latency — deliberately left alone until this runs against real traffic, since it's a shared constant (`ClassifyStageTimeout` derives from it and bounds a Kafka consumer).

Embeddings staying on DMR isn't an oversight: moving them too would mean a pgvector dimension migration and re-embedding every stored chunk — a separate, much bigger job, not undertaken here.

## Swapped to franz-go partway through

Started with segmentio/kafka-go (pure Go, no cgo, the obvious default), switched to franz-go mid-build. Turned out better: consumer-group join went from tens of seconds to sub-second in integration tests, and `kadm.CreateTopics` beats kafka-go's `Conn.Controller()` dance. The port interfaces (`EventPublisher`/`EventConsumer`) already isolated the rest of the app from this, so the swap only touched the two adapter packages.

## PDF text extraction: pdftotext, with an OCR fallback for scanned pages

`pdftotext` (shelled out via `os/exec`, `apt-get install poppler-utils`) over a cgo PDF library (considered `go-fitz`/MuPDF) — cgo plus Docker builds is a classic "works on my machine" problem, and the top priority here is `docker compose up` just working, no exceptions.

Scanned/image-only PDFs originally weren't handled at all, and extracted to a lone form-feed byte that passed through the whole pipeline as a junk "success" — `StatusDone`, empty fields, zero chunks, no visible error. Fixed with an OCR fallback: `pdftoppm` rasterizes pages, `tesseract` OCRs each one, both shelled out the same way `pdftotext` already is (no cgo, no daemon, no HTTP interface to run). If both pdftotext and OCR come up short, extraction now returns `ErrNoExtractableText` and the resume is marked `FAILED` with a clear reason instead of `DONE` with junk data. Tuning constants (`MinExtractedTextChars=100`, `MaxOCRPages=5`, `OCRRasterDPI=200`) came from measuring a real scanned resume, not guessing: ~1.3s/page, clean output at 200 DPI.

Known gap, out of scope: resumes already marked `DONE` with blank extractions from before this fix aren't backfilled.

## Kafka for async ingestion, not synchronous processing on upload

Could've processed everything inline during the upload request. Went with Kafka for a better UX (immediate response instead of waiting on LLM calls) and because it's the right async pattern here — even though it's more infrastructure (broker, consumer group, idempotency handling) than a ~1000-row workload strictly needs.

## Shared disk volume for PDFs, not object storage or a bytea column

A Compose volume is enough at this scale and adds no extra moving parts. Object storage (MinIO) is the obvious next step if workers ever need genuinely separate hosts — not needed yet.

## No ANN index on the embeddings column

At ~1000 resumes, a sequential scan is exact and comfortably under 10ms. An HNSW/IVFFlat index would trade correctness for speed not needed yet — revisit past ~10k chunks or if p99 search latency actually becomes a problem.

## Chunking: fixed-size recursive splitting, sized in words with a token-ratio margin

Recursive fixed-size splitting (no section-header-aware chunking, no overlap) outperformed semantic/section-based chunking on retrieval accuracy in 2026 RAG benchmarks, with no measurable benefit from overlap — simpler and better-benchmarked.

The size constant was originally named and sized (512) as if it bounded model tokens, but the splitter counts words, and a 1:1 word:token ratio is never actually true — three real (OCR'd/designer-style) resumes hit a measured 1.36-1.58x ratio, driven by URLs and glyph noise fragmenting into extra subword tokens, and overflowed the embedding model's 512-token batch size in production. Docker Model Runner has no tokenize endpoint to check against directly, so fixed by renaming the constant to what it is (`ChunkSizeWords`) and dropping it to 256 — comfortable margin above the worst measured ratio, verified by replaying the actual failed resumes through the fixed pipeline.

## Skills filter is AND, not OR

A recruiter searching "must-have" skills doesn't want candidates missing one of them showing up anyway. Array containment (AND), stated explicitly in the API.

## Hexagonal architecture over a flatter package layout

Could've gone flatter — extract/model/store/kafka/api. This project's point is to show architectural judgment, not just "it works": the dependency rule (domain knows nothing about anything, adapters point inward, `service` owns the port interfaces) is deliberately on display, even though it's more directories and interfaces than a project this size strictly needs.

## domain.ErrNotFound as one sentinel, mapped to a status code only at the HTTP layer

One sentinel error, wrapped with `%w` at every layer so `errors.Is` keeps working regardless of depth. Only the HTTP driver decides 404 vs 500 — a repository or use case shouldn't get to make that call, or a genuine DB outage could look identical to "this resume doesn't exist."

## Filename handling: strip to base name, then index-prefix on disk

An uploaded filename is attacker-controlled: `../../etc/passwd` must not escape the batch directory, and two people uploading `resume.pdf` in the same batch must not clobber each other. `sanitizeFilename` strips to the base component and rejects the remaining degenerate cases (`""`, `.`, `..`); the stored name is then index-prefixed so duplicates never collide.

## Splitting the pipeline into 3 Kafka stages + a redrive sweeper

Production bug: an LLM extraction timeout's follow-up "mark this FAILED" write reused the same already-expired context and failed too, leaving 11 rows permanently wedged at `PROCESSING` — and Kafka still committed the offset, since `consumer.go`'s invariant ("failure is already durably recorded") silently broke.

Fixed as one scope: a shared `writeStatus` helper wraps every terminal status write in a detached context with its own short timeout, so a FAILED/DONE write always gets a fresh budget; a new sentinel `ErrStatusNotRecorded` lets the consumer skip committing the offset when even that write fails. The monolithic per-resume pipeline was split into 3 Kafka-topic stages (extract → classify → embed) so a slow LLM call can't block extraction or embedding for other resumes. Splitting introduces its own crash gap (durably parked mid-stage, nothing left to republish it), closed by a redrive sweeper that reclaims resumes stale past `SweepStaleAfter` and republishes each to its current `stage`'s topic, capping at `MaxRedrives` (5) before giving up and marking it FAILED.

Two non-obvious things worth keeping in mind: the terminal-skip guard in every stage's `Run` checks `Resume.Status` (DONE/FAILED), never `Resume.Stage` — the embed stage has no `AdvanceStage` call after it (its terminal write is `writeStatus(DONE)`), so a crash between `SaveChunks` and that write leaves a row at `stage=EMBED, status=PROCESSING`; guarding on Stage instead of Status would make the sweeper's redelivery a silent no-op and deadlock the row forever. And `SweepStaleAfter` is defined relative to `EmbedStageTimeout` (its largest input), not a flat constant — a flat 5-minute value once double-claimed a resume that was still legitimately embedding a large chunk count, burning its redrive budget for doing nothing wrong.

Per-stage consumer timeouts replace the old single blanket `ResumeProcessingTimeout`: extract gets 30s, classify derives from `MaxExtractionRetries × LLMAttemptTimeout` plus slack, and embed's budget is computed at runtime from the actual chunk count rather than a fixed value, since embedding cost scales with document length. The sweeper's claim is one atomic `UPDATE ... RETURNING`, not a lock-then-release read, so two worker replicas' sweepers can't double-claim the same stale row.

## Swapped the migration runner to golang-migrate partway through

Had a hand-rolled runner with a self-written Postgres advisory lock (so `app`/`worker` starting concurrently against a fresh DB wouldn't race on `CREATE EXTENSION IF NOT EXISTS`). golang-migrate's Postgres driver already takes its own advisory lock for the whole run, so that problem is now a library's job, not maintained code — and migrations are proper up/down pairs, which the old runner never had.

## Server-rendered Go templates + htmx for the web UI, not a JS framework

Wanted a browser UI without a build step, a bundler, or a second language runtime. `html/template` + htmx gives interactivity (manual refresh button, fragment swaps) with no hand-written JS. Templates, CSS, and a vendored `htmx.min.js` (not CDN-loaded — the app shouldn't depend on outbound network at runtime) are all embedded into the binary via `embed.FS`; no Dockerfile change needed.

Refresh is a manual button, not background polling — a resume takes tens of seconds to minutes to move through the pipeline, and polling every few seconds would mostly hit the DB for nothing. One real gotcha: htmx won't swap a non-2xx response into its target by default, so a fragment endpoint's use-case error has to render inline at 200 instead of through the normal error page (documented in code on the `web` package itself, not repeated here).

Upload's multipart parsing (body-size bound, file-count bound) is the one helper shared between the JSON API and the web UI, despite this codebase otherwise duplicating small amounts of logic per adapter on purpose — `MaxUploadBytes`/`MaxUploadFiles` are security-relevant bounds, and two independent copies of a security bound is exactly the kind of thing that quietly drifts apart.

## The web UI maps errors to a generic message + slug; the JSON API still returns raw errors

The web UI's error pages originally rendered `err.Error()` directly, matching the JSON API's convention — until a review caught that this is a real leak on an unauthenticated HTML page in a way it isn't for a JSON API meant for a trusted client (a DB outage would put hostnames/connection strings straight into a browser tab). Fixed with one function, `classifyError`, that every web error path goes through: a status, a stable slug (`not-found`/`internal-error`), and a generic message — the real error still gets logged via `slog`, just no longer rendered to the page.

Deliberately scoped to the web UI only. The JSON API's existing test suite asserts the raw-error body as expected behavior, and changing that now is a real regression risk for a suite already used as a correctness anchor — revisiting it is a call for whoever owns that API's actual threat model, tracked as a known gap in `docs/production-readiness.md`, not changed quietly as a side effect of fixing the web UI.

For the same reason (small, low-value win vs. a real regression/consistency risk), the three near-identical Kafka publisher ports (`EventPublisher`/`ExtractedPublisher`/`ClassifiedPublisher`, all implemented by one `kafka.Producer`) and the 11-method `ResumeRepository` port were considered for consolidation during a later structural cleanup pass and rejected — the evidence for pain was mostly test-fake size, not reviewer comprehension, and splitting/merging either would trade one kind of interface noise for another. That same cleanup pass did unify 4 *duplicate* interface declarations (the same shape declared once in the JSON API driver and again, sometimes under a different name, in the web driver) into 3 shared interfaces declared once in `service` — a real duplication, unlike the ports above, which were never duplicated, just fine-grained.

## Processing tab: a GROUP BY on resumes, not a separate batches table

`resumes.batch_id` already has an index, so `ListBatches` is a `GROUP BY` with per-status counts — no schema change, no dedicated `batches` table with its own write path to justify. No JSON API route was added for it either: every other use case here is called from both the JSON API and the web UI, but `ListBatches` only has a web consumer, and adding a route with no caller would be exactly the kind of build-it-for-symmetry addition this project otherwise avoids.

## Viewing the actual resume file from search results, restricted to `.pdf` uploads

Search results carried a resume's ID but nothing let you open the file. Adding `GET /resumes/:id/file` surfaced that uploads had no extension/content-type allowlist at all — fine while nothing served the bytes back to a browser, not fine the moment a "view" endpoint exists (an uploaded `.html`/`.svg` served inline would be stored XSS against this app's own origin). Two independent fixes: uploads now reject any non-`.pdf` filename at the same validation pass that already rejects unsafe ones, and the file handler itself still only serves `.pdf`-named files inline (with `nosniff`) regardless — a resume row that predates the check, or any future loosening, doesn't get to rely on the upload-time invariant alone. The file handler also got its own error mapping (404 stays plain, 500 logs and returns a generic message) for the same reason the web UI did — it's now a link a human clicks directly from a browser, not a trusted JSON client.

## Renaming `distance` to `match_percentage`

Raw pgvector cosine distance is lower-is-better, which is a footgun on a UI column literally waiting to be misread as a score. Fixed at the DTO boundary only (`match_percentage = round((1 - distance) * 100)`, clamped to [0, 100]) — the repository/domain layer's `best_distance` naming is accurate to what SQL actually computes and didn't need to change. Measured before shipping: a relevant query scores real matches 55-71%; a deliberately irrelevant one still scores 42-48%, not near 0% (the embedding model doesn't give unrelated text near-zero cosine similarity) — worth knowing if this field is ever used for a "only show >X%" filter, since the real floor for "no relationship" is the high-40s, not 0. Ranking itself is unaffected either way; this was a display fix, not a scoring change.
