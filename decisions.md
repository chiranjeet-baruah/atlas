# Decisions

Just a running log of the calls I made while building this, and why.

## Why Go, not Python

I went back and forth on this one for a bit. Python has the obvious ecosystem advantage for PDF/NLP stuff, and honestly a Go+Python split (API in Go, ingestion worker in Python) crossed my mind too. But I ended up doing the whole thing in Go. One toolchain, and the concurrency model just fits a worker pool naturally. The tradeoff is I save up Python's PDF libraries and I'm using `pdftotext` instead.

## Docker Model Runner instead of an Ollama sidecar or a hosted API

Wanted the whole thing to be `docker compose up` and done, no separate model server to wire up, no API keys, no cost. DMR's native `models:` block in Compose does exactly that.

I didn't just assume the env var names/URL shape. I actually spun up a throwaway alpine container with `models: [llm]` attached and did `env | grep -i llm` against it. Good thing I checked: the injected vars are `LLM_URL`/`LLM_MODEL` (and `EMBED_URL`/`EMBED_MODEL` for a service named `embed`), which matched what I expected, but the URL itself is `http://model-runner.docker.internal/v1/` — trailing slash AND already has `/v1`, not `/engines/v1` like I'd guessed originally. Would've been a double-slash bug if I hadn't checked. `ModelClient.New` trims the trailing slash now regardless, so it doesn't matter either way going forward.

One honest caveat: the model I actually committed as the default, `ai/qwen3` (5GB), kept failing to pull; got a `416 Requested Range Not Satisfiable` from the registry partway through, more than once. Never got a clean full pull of it in this environment. So the full pipeline (upload → kafka → extract → chunk → embed → search) was actually verified end-to-end using `ai/llama3.2` instead, swapped in locally via a gitignored `docker-compose.override.yml` that never got committed. `ai/qwen3` stays as the committed default because the client code path is identical either way, but I want to be upfront that its specific weights were never actually exercised by me.

Also gave up some extraction quality/speed by not using a hosted API — small local models are shakier at sticking to a JSON schema. Mitigated that with retries + stripping markdown fences before parsing, see below.

## One HTTP client, not one per model provider

DMR, Ollama, OpenAI, vLLM — they all speak basically the same OpenAI-compatible chat/embeddings shape. Didn't see the point in building three near-identical adapters for that. One client, parameterized by base URL + model name, and swapping providers later is just an env var change. Skipped the abstraction layer since it would've existed on paper only.

## Swapped to franz-go partway through

Started with segmentio/kafka-go — pure Go, no cgo, everyone uses it. Got told to switch to franz-go mid-build and honestly it turned out better: consumer-group join is noticeably faster (my integration tests went from tens of seconds to sub-second per subtest, which I did not expect), and the `kadm` package's `CreateTopics` is a lot cleaner than kafka-go's `Conn.Controller()` dance. Because the port interfaces (`EventPublisher`/`EventConsumer`) were already there insulating the rest of the app from this, the swap only touched the two adapter packages and their tests. Nothing else even noticed.

## Skipping OCR

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

## Swapped the migration runner to golang-migrate partway through

Had a hand-rolled migration runner originally — read the .sql files, run them in order, with a Postgres advisory lock I wrote myself so `app` and `worker` starting up at the same time against a fresh DB wouldn't race on `CREATE EXTENSION IF NOT EXISTS`. Got told to use golang-migrate instead, and once I looked into it, its Postgres driver already takes its own advisory lock for the whole migration run — so the exact problem my hand-rolled lock existed for is now just handled by a library instead of code I have to maintain. Migrations are now proper up/down pairs too, which the old runner never had.
