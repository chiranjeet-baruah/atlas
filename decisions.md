# Decisions

Just a running log of the calls I made while building this, and why.

## Why Go, not Python

I went back and forth on this one for a bit. Python has the obvious ecosystem advantage for PDF/NLP stuff, and honestly a Go+Python split (API in Go, ingestion worker in Python) crossed my mind too. But I ended up doing the whole thing in Go. One toolchain, and the concurrency model just fits a worker pool naturally. The tradeoff is I save up Python's PDF libraries and I'm using `pdftotext` instead.

## Docker Model Runner instead of an Ollama sidecar or a hosted API

Wanted the whole thing to be `docker compose up` and done, no separate model server to wire up, no API keys, no cost. DMR's native `models:` block in Compose does exactly that.

I didn't just assume the env var names/URL shape. I actually spun up a throwaway alpine container with `models: [llm]` attached and did `env | grep -i llm` against it. Good thing I checked: the injected vars are `LLM_URL`/`LLM_MODEL` (and `EMBED_URL`/`EMBED_MODEL` for a service named `embed`), which matched what I expected, but the URL itself is `http://model-runner.docker.internal/v1/` — trailing slash AND already has `/v1`, not `/engines/v1` like I'd guessed originally. Would've been a double-slash bug if I hadn't checked. `ModelClient.New` trims the trailing slash now regardless, so it doesn't matter either way going forward.

One honest caveat, now resolved: the model originally committed as the default, `ai/qwen3` (5GB), kept failing to pull — got a `416 Requested Range Not Satisfiable` from the registry partway through, more than once, never a clean full pull in this environment. The full pipeline (upload → kafka → extract → chunk → embed → search) was actually verified end-to-end using `ai/llama3.2` instead, swapped in locally via a gitignored `docker-compose.override.yml` at the time. Rather than keep committing a default that doesn't reliably pull, switched the committed default itself to `docker.io/ai/llama3.2:latest` — the override workaround is gone, and the model actually exercised during development is now the one everyone gets.

Also gave up some extraction quality/speed by not using a hosted API — small local models are shakier at sticking to a JSON schema. Mitigated that with retries + stripping markdown fences before parsing, see below.

## One HTTP client, not one per model provider

DMR, Ollama, OpenAI, vLLM — they all speak basically the same OpenAI-compatible chat/embeddings shape. Didn't see the point in building three near-identical adapters for that. One client, parameterized by base URL + model name, and swapping providers later is just an env var change. Skipped the abstraction layer since it would've existed on paper only.

## Swapped to franz-go partway through

Started with segmentio/kafka-go — pure Go, no cgo, everyone uses it. Got told to switch to franz-go mid-build and honestly it turned out better: consumer-group join is noticeably faster (my integration tests went from tens of seconds to sub-second per subtest, which I did not expect), and the `kadm` package's `CreateTopics` is a lot cleaner than kafka-go's `Conn.Controller()` dance. Because the port interfaces (`EventPublisher`/`EventConsumer`) were already there insulating the rest of the app from this, the swap only touched the two adapter packages and their tests. Nothing else even noticed.

## OCR fallback for scanned PDFs

Originally skipped OCR entirely (see below, kept for context on what was rejected and why). Revisited after silent-failure reports: scanned/image-only PDFs were extracting to a lone form-feed byte and passing through the whole pipeline as a junk "success" (StatusDone, empty fields, zero chunks) with no visible error.

Added a fallback: pdftoppm rasterizes pages, tesseract OCRs each one, shelled out via os/exec exactly like pdftotext already is. This sidesteps both original objections — no cgo (gosseract was the rejected option, not the tesseract CLI), and no need for an HTTP interface (the earlier objection was about a bare "docker run" tesseract image; shelling out from the existing worker process needs no daemon or API at all). If pdftotext and OCR both come up short, ExtractText now returns ErrNoExtractableText instead of silently succeeding — the resume is marked FAILED with a clear reason instead of DONE with junk data.

The three tuning constants (`MinExtractedTextChars`, `MaxOCRPages`, `OCRRasterDPI`) were set from a real measurement, not guessed: rasterizing + OCRing a genuinely scanned resume at 200 DPI took ~1.3s/page and produced clean, accurate text, so 200 DPI stood as-is and a 5-page cap keeps worst case well inside `ResumeProcessingTimeout`. The 100-char threshold caught a real bug during that same pass — the existing `testdata/sample.pdf` test fixture was a 73-char stub ("John Doe" plus one line), which is shorter than any real resume but also shorter than the new threshold, so it started wrongly triggering the OCR fallback on a perfectly good text-based PDF. Fixed by regenerating that fixture with realistic multi-line resume content (still contains "John Doe" for the existing assertion) rather than lowering the threshold to accommodate an unrealistic fixture.

Known gap: resumes already marked DONE with blank extractions from before this fix are not backfilled. Out of scope for this change; would need a separate reprocessing pass if addressed later.

Considered running poppler/tesseract as separate Docker Hub images (`minidocks/poppler`, `jitesoft/tesseract-ocr`) instead of apt-get. Rejected: both are ephemeral CLI-only containers, not services. Using them as `docker run` sidecars needs a mounted Docker socket (privilege-escalation risk, contradicts "no extra moving parts"); copying single binaries via multi-stage COPY risks musl/glibc or cross-distro shared-library mismatches against the debian:bookworm-slim final image. Plain `apt-get install tesseract-ocr` next to the existing poppler-utils line avoids all of this.

## Skipping OCR (superseded above)

Scope was text-based PDFs from the start. Looked briefly at `gosseract` (cgo tesseract binding) and a tesseract Docker image, but the image is CLI-only with no HTTP interface, which doesn't fit a long-running compose service well. Scanned resumes are just not supported, documented as future work.

## pdftotext over a cgo PDF library

Considered go-fitz (MuPDF binding) briefly. cgo + Docker builds is a classic source of "works on my machine" pain, and my top priority here is "reviewer runs `docker compose up` and it just works", no exceptions. `pdftotext` is one `apt-get install poppler-utils` away and has been battle-tested for decades.

## Kafka for async ingestion instead of just processing synchronously on upload

Could've just processed everything inline during the upload request. Went with Kafka instead for two reasons: better UX (client gets an immediate response instead of waiting on LLM calls), and honestly, it's also a go-to async pattern based on my experience. I know this is more infrastructure than a ~1000 row workload strictly needs: broker, consumer group, idempotency handling.

## Shared disk volume for PDFs, not object storage or a bytea column

MinIO would've been the "correct" choice if I actually wanted multi-host workers, and a Postgres bytea column would've been the simplest. Went with a plain shared Compose volume because at this scale it's enough and doesn't add any extra moving parts. If this ever needed genuinely separate hosts for workers, object storage is the obvious next step — just not needed yet.

## No ANN index on the embeddings column

At ~1000 resumes with a handful of chunks each, a sequential scan is exact and still comfortably under 10ms. An HNSW/IVFFlat index would trade correctness for speed I don't need right now. Will revisit if this ever gets past ~10k chunks or if p99 search latency actually becomes a real problem, but right now it'd just be premature.

## Chunking: fixed-size recursive splitting, no overlap

My gut instinct going in was section-header-aware chunking. Checked some 2026 RAG benchmarks before committing to that though, and recursive fixed-size splitting (~512 tokens) actually outperformed semantic/section-based chunking on retrieval accuracy in those studies, and overlap didn't show any measurable benefit either. So I went with the simpler approach, which happened to also be the better-benchmarked one.

## Skills filter is AND, not OR

A recruiter searching "must-have" skills doesn't want candidates missing one of them showing up anyway. Went with array containment (AND) rather than "at least N of," and made sure that's stated explicitly in the API.

## Hexagonal architecture over a flatter package layout

Could've gone with a much flatter structure — extract/model/store/kafka/api. But this project's whole point is to show architectural judgment, not just "it works." The dependency rule (domain knows nothing about anything, adapters point inward, `service` owns the port interfaces) is exactly the thing I wanted on display. Yes, it's more directories and interfaces than a project this size strictly needs. That's the tradeoff I'm making on purpose.

## domain.ErrNotFound as one sentinel, mapped to a status code only at the HTTP layer

Could've done boolean-returning lookups, or mapped "not found" ad hoc inside the Postgres repository per query. Went with a single sentinel error instead, wrapped with `%w` at every layer, so `errors.Is` keeps working no matter how many layers it passes through. The HTTP driver is the only place that decides 404 vs 500. A repository or use case shouldn't get to make that call. This matters because otherwise a genuine DB outage could accidentally look identical to "this resume doesn't exist," which would be a bad time for whoever's debugging an incident.

## Filename handling: strip to base name, then index-prefix on disk

An uploaded filename is attacker-controlled input. `../../etc/passwd` has to not escape the batch directory, and two people both uploading `resume.pdf` in the same batch can't be allowed to silently clobber each other. `sanitizeFilename` strips down to the base component and only rejects the truly degenerate cases (`""`, `.`, `..`) that are still unsafe after stripping. Then I prefix the stored filename with its index in the batch, so duplicates never collide. Both of these are actually asserted in tests.

## Splitting the pipeline into 3 Kafka stages + a redrive sweeper

Real worker logs surfaced a production bug: `context deadline exceeded` during LLM extraction, and the follow-up "mark this FAILED" write failed too — because it reused the same already-expired `ctx`. Checked the live DB and found 11 rows permanently wedged at `PROCESSING` with empty `raw_text`, never reaching `FAILED`. Also broke `consumer.go`'s stated invariant ("failure is already durably recorded, so committing the offset is safe") — since recording could silently fail, Kafka committed the offset anyway and the resume was never retried.

Fixed three things at once, deliberately as one scope rather than three separate changes:

1. **Context-lifetime fix.** A shared `writeStatus` helper (`internal/service/status_write.go`) wraps every terminal status write in `context.WithoutCancel(ctx)` + a fresh `WithTimeout`, so a FAILED/DONE write always gets its own short budget instead of inheriting whatever's left (or already expired) on the caller's context. A new sentinel, `domain.ErrStatusNotRecorded`, distinguishes "processing failed, and I couldn't even record that" from an ordinary processing failure — the Kafka consumer checks for it via `errors.Is` and skips committing the offset in that case (see point 3 below for the actual commit semantics).

2. **Per-attempt LLM timeout.** `modelclient.Client.Extract`'s 3 retries used to share one deadline, so a slow first attempt could eat the whole budget and leave nothing for attempts 2 and 3. Now each attempt gets its own `constants.LLMAttemptTimeout`. That constant was measured, not guessed: 3 real calls to the committed default model (`ai/llama3.2`, see above) with a real (short) resume took 24.97s/1.53s/1.86s — the first call pays for loading the model into Docker Model Runner, subsequent calls hit the warm model and finish in ~2s. The cold-start cost recurs on every model eviction or worker restart, so the budget is sized off that worst case: 60s leaves ~35s of margin over the observed cold start. Re-measure if the model changes again or production resumes run substantially longer than this short sample.

**Re-measured after later swapping the committed LLM from `ai/qwen3` to `ai/llama3.2` (see the "Docker Model Runner" section above):** the original 60s figure for `LLMAttemptTimeout` was measured against qwen3 — a reasoning model whose latency comes from chain-of-thought (36.9s/42.4s/45.7s, 888-1147 completion tokens). llama3.2 is not a reasoning model and is dramatically faster warm (~2s), but pays a ~25s cold-start cost the first time it's called after being loaded/evicted. 60s still comfortably covers that cold-start case, so the constant didn't need to change — only the comment explaining *why* did, since blaming chain-of-thought for a non-reasoning model would have been a stale, misleading rationale for the next person reading it.

3. **Split the monolithic per-resume pipeline into 3 Kafka-topic stages** (extract → classify → embed), so a slow/unreliable LLM call can't block text extraction or embedding for other resumes on the same consumer. Chaining stages like this ("DB write, then publish to the next topic") introduces a new, structurally identical failure mode if the process crashes between those two steps — the resume gets durably parked at some intermediate stage with nothing left to ever republish it. A redrive sweeper (`internal/service/redrive_sweep.go`) closes that gap: it periodically claims resumes that haven't progressed in `SweepStaleAfter` and republishes each to the topic matching its current `stage` column (a new migration, `0002_stage_tracking`), or marks it FAILED once it's been redriven `MaxRedrives` (5) times without completing — a poison-pill cutoff so a genuinely broken PDF doesn't retry forever.

**Caught in a post-implementation advisor review, before commit:** `SweepStaleAfter` was initially a flat 5 minutes, picked independently of the stage timeouts. But the embed stage never advances `updated_at` between its initial `UpdateStatus(PROCESSING)` and its final `SaveChunks`/`writeStatus(DONE)` — unlike extract/classify, it has no mid-stage `AdvanceStage` call. A resume embedding ≥20 chunks (`20×15s EmbedAttemptTimeout > 300s`) would still be legitimately processing when a 5-minute sweep claimed it, double-embedding it and burning a `redrive_count` for doing nothing wrong — and doing that 5 times in a row would mark a perfectly healthy large resume FAILED. Fixed by defining `SweepStaleAfter` relative to `EmbedStageTimeout` (`EmbedStageTimeout + 2*time.Minute`, ~14m45s) instead of a standalone constant, so it's structurally guaranteed to exceed every stage's worst case and can't drift out of sync if `MaxEmbedChunks`/`EmbedAttemptTimeout` change later. Also measured (not guessed) the actual `/v1/embeddings` call latency against the local model runner before trusting `EmbedAttemptTimeout = 15s`: 31ms warm, 1.24s cold-start on a real ~80-word chunk — over 10x margin even cold, so unlike the LLM extraction path, embed needed no retry loop.

The claim itself is one atomic `UPDATE ... WHERE id IN (SELECT ...) RETURNING ...`, not a `SELECT ... FOR UPDATE SKIP LOCKED` — the row lock from a SKIP LOCKED read releases before the Kafka publish happens (which is outside the transaction), so two worker replicas' sweepers could still double-claim the same row between the unlock and the publish. The atomic claim increments `redrive_count` and bumps `updated_at` in the same statement that selects the rows, so a second sweeper's claim query — filtered on `updated_at < now() - staleAfter` — simply won't see that row again until the next `staleAfter` window. Verified this directly against the live DB rather than just in a testcontainer: ran `ClaimStaleForRedrive` against the real stuck rows (11 `PROCESSING` + 19 `PENDING`, both non-terminal and stale, all defaulting to `stage='EXTRACT'` after the migration) — the first call claimed all 30 and bumped their `redrive_count` to 1; an immediate second call claimed 0, confirming the double-claim guard actually works, not just in theory.

One nuance the sweeper's own doc comment calls out explicitly: it *drains* stuck resumes by re-running them through the pipeline from whichever stage they're parked at — it does not "recover" them for free. If the underlying cause (e.g. an LLM timeout) is still happening, a redriven resume can wedge again, burning one `redrive_count` per `SweepStaleAfter` cycle until `MaxRedrives` flips it to FAILED — a bounded, visible failure instead of a silent permanent one, but not magic.

Also worth calling out since it's easy to get backwards: the idempotent-redelivery guard in every stage's `Run` checks `Resume.Status` (terminal = DONE or FAILED), never `Resume.Stage`. The embed stage in particular has no `AdvanceStage` call after it — it's the last stage, so its terminal write is `writeStatus(DONE)` instead. If a crash happens between `SaveChunks` and that DONE write, the row sits at `stage=EMBED`, `status=PROCESSING`, and the sweeper redrives it back onto the embed topic. Guarding on `Stage` instead of `Status` here would make that redelivery a silent no-op (since it "already reached" stage EMBED) and the row would never reach DONE — a permanent deadlock, worse than the bug this whole change exists to fix. Guarding on `Status` makes the redrive safely reprocess instead: `SaveChunks`'s `ON CONFLICT (resume_id, chunk_index) DO UPDATE` upsert (already true before this change) makes redundant re-embedding wasted work, not a correctness problem.

Per-stage consumer timeouts replace the old single 2-minute blanket `ResumeProcessingTimeout`: extract gets 30s (pdftotext is fast; OCR's measured worst case is ~6.5s), classify gets `3×LLMAttemptTimeout + slack` (~210s), and embed's real budget is computed at runtime from the actual chunk count (`len(chunks)×EmbedAttemptTimeout + slack`) rather than a fixed constant, since embedding cost scales with document length — a fixed budget would just reintroduce "stage times out mid-work" for the longest documents. `kafka.NewConsumer` gained a `handlerTimeout` parameter so this lives on the consumer (wrapping every handler call itself), not something each use case has to remember to set up.

## Swapped the migration runner to golang-migrate partway through

Had a hand-rolled migration runner originally — read the .sql files, run them in order, with a Postgres advisory lock I wrote myself so `app` and `worker` starting up at the same time against a fresh DB wouldn't race on `CREATE EXTENSION IF NOT EXISTS`. Got told to use golang-migrate instead, and once I looked into it, its Postgres driver already takes its own advisory lock for the whole migration run — so the exact problem my hand-rolled lock existed for is now just handled by a library instead of code I have to maintain. Migrations are now proper up/down pairs too, which the old runner never had.
