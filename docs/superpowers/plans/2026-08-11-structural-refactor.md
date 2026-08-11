# Structural Cleanup Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce reviewer cognitive load in the `atlas` codebase — stale comments, duplicated interface declarations, duplicated pipeline-stage boilerplate, unnecessarily exported functions, and one domain predicate living in the wrong package — with zero behavior change.

**Architecture:** No architectural change. Six independent, mechanical edits: a comment-cleanup pass, one constant reorder, two unexports, one domain-logic relocation, one interface-declaration unification across two driver packages, and one shared-helper extraction across three pipeline use cases. Every change preserves exact current behavior; existing tests must pass unmodified (only import paths / call syntax may change, never assertions).

**Tech Stack:** Go 1.26, Gin, pgx/pgxpool, franz-go (Kafka), no new dependencies.

## Global Constraints

- **Exact behavior preservation.** No task may change an error message string, an HTTP status code, a response body shape, a log line, or a control-flow decision. If a test's assertions need to change to pass, stop — that means behavior shifted, which is out of scope.
- **No placeholders.** Every task below contains the literal code to write — nothing is left as "TODO" for the implementer.
- **Verify with `GOTOOLCHAIN=auto`.** This machine's installed Go (1.26.0) is older than `go.mod`'s floor (1.26.5); every `go build`/`go vet`/`go test` command in this plan must be prefixed `GOTOOLCHAIN=auto` or it fails on a toolchain-fetch error unrelated to the code.
- **Integration tests need a live stack.** `make test-integration` requires Postgres + Kafka running. Tasks 3, 4, and 6 touch `internal/adapter/driven/postgres` or `internal/domain`/`internal/service` — run the integration suite for those if the stack is available; if it isn't, say so explicitly in the task's commit message or handoff rather than silently skipping verification.
- **One commit per task**, in the order given (ascending risk) — do not batch tasks into one commit.

---

### Task 1: Stale/repeated comment cleanup

**Files:**
- Modify: `internal/constants/constants.go:207-212`
- Modify: `internal/adapter/driven/pdf/extractor.go:97-101`
- Modify: `internal/dto/dto.go:74-81`
- Modify: `internal/adapter/driver/multipartform/parse.go:27-32`
- Modify: `internal/adapter/driver/web/processing_test.go:99-102`
- Modify: `internal/adapter/driver/http/upload_handler_test.go:126-129`
- Modify: `internal/adapter/driver/http/search_handler_test.go:93-96`
- Modify: `internal/adapter/driver/web/batch.go:45-51`
- Modify: `internal/adapter/driver/web/processing.go:65-71`
- Modify: `internal/adapter/driver/web/search.go:78-83`
- Create: `internal/adapter/driver/web/doc.go`

**Interfaces:** None — comment-only changes, no code signatures touched.

- [ ] **Step 1: Confirm the baseline is green**

Run: `GOTOOLCHAIN=auto go build ./... && GOTOOLCHAIN=auto go test ./... 2>&1 | tail -20`
Expected: build succeeds, all packages report `ok`.

- [ ] **Step 2: Fix the two stale `ResumeProcessingTimeout` references**

In `internal/constants/constants.go`, replace lines 207-212:

```go
	// MaxOCRPages bounds how many pages the OCR fallback rasterizes and
	// reads. OCR costs roughly a second per page (measured: ~1.3s/page
	// combined pdftoppm+tesseract), so an unbounded page count risks
	// blowing ResumeProcessingTimeout on a long scanned document. Resumes
	// are essentially never longer than this in practice.
	MaxOCRPages = 5
```

with:

```go
	// MaxOCRPages bounds how many pages the OCR fallback rasterizes and
	// reads. OCR costs roughly a second per page (measured: ~1.3s/page
	// combined pdftoppm+tesseract), so an unbounded page count risks
	// blowing ExtractStageTimeout on a long scanned document. Resumes
	// are essentially never longer than this in practice.
	MaxOCRPages = 5
```

In `internal/adapter/driven/pdf/extractor.go`, replace lines 97-101:

```go
// extractWithOCR rasterizes the first constants.MaxOCRPages pages to PNGs
// and runs tesseract on each, in page order, then joins the per-page text
// with blank lines. Bounding the page count keeps OCR — on the order of
// a second per page — inside ResumeProcessingTimeout even for long
// documents.
```

with:

```go
// extractWithOCR rasterizes the first constants.MaxOCRPages pages to PNGs
// and runs tesseract on each, in page order, then joins the per-page text
// with blank lines. Bounding the page count keeps OCR — on the order of
// a second per page — inside constants.ExtractStageTimeout even for long
// documents.
```

- [ ] **Step 3: Drop the "old distance field" aside in `dto.go`**

In `internal/dto/dto.go`, replace lines 74-81:

```go
	// MatchPercentage is 0-100, higher means a better match. Derived from
	// pgvector cosine distance (repository.go's Search, best_distance = 1 -
	// cosine similarity) via matchPercentage below. Unlike raw distance,
	// higher-is-better here actually matches the name, so — unlike the old
	// "distance" field — this one is safe to think of the way its name
	// suggests. Results are still sorted best-first server-side (ORDER BY
	// best_distance ASC, which is also highest-percentage-first); a client
	// never needs to re-sort this.
```

with:

```go
	// MatchPercentage is 0-100, higher means a better match. Derived from
	// pgvector cosine distance (repository.go's Search, best_distance = 1 -
	// cosine similarity) via matchPercentage below. Results are already
	// sorted best-first server-side (ORDER BY best_distance ASC, which is
	// also highest-percentage-first); a client never needs to re-sort this.
```

- [ ] **Step 4: Fix the "planned adapter" aside in `multipartform/parse.go`**

In `internal/adapter/driver/multipartform/parse.go`, replace lines 27-32:

```go
// ParseUploadFiles bounds the request body to constants.MaxUploadBytes,
// parses the "files" multipart field, and reads every part fully into
// memory. internal/adapter/driver/http calls this; a planned web driver
// adapter (a later task in this plan) will too, so the two upload entry
// points will enforce identical limits — MaxUploadBytes and MaxUploadFiles
// exist in exactly one place.
```

with:

```go
// ParseUploadFiles bounds the request body to constants.MaxUploadBytes,
// parses the "files" multipart field, and reads every part fully into
// memory. Both internal/adapter/driver/http and internal/adapter/driver/web
// call this for their respective upload entry points, so
// MaxUploadBytes/MaxUploadFiles are enforced identically by both and exist
// in exactly one place.
```

- [ ] **Step 5: Rewrite the three commit-hash-referencing test comments**

In `internal/adapter/driver/web/processing_test.go`, replace lines 99-102:

```go
		{
			// Locks in the exact gap fixed by commit 17d016b for batch/search:
			// the error message alone isn't enough, the slug reference must
			// also survive into the rendered fragment.
			name:           "use-case error renders inline with its slug, not a 500 page",
```

with:

```go
		{
			// A use-case error must render inline inside the fragment (not a
			// 500 page) with both the generic message AND the error-slug
			// reference surviving into the HTML — htmx won't swap a non-2xx
			// response into its target, so a full-page error response here
			// would leave the fragment's target empty.
			name:           "use-case error renders inline with its slug, not a 500 page",
```

In `internal/adapter/driver/http/upload_handler_test.go`, replace lines 126-129:

```go
// TestUploadHandler_RejectsOversizedBody proves the fix for the unbounded
// request-body finding: a request larger than constants.MaxUploadBytes
// must be rejected with 413 before it's fully read into memory, not
// accepted and processed.
```

with:

```go
// TestUploadHandler_RejectsOversizedBody asserts that a request larger than
// constants.MaxUploadBytes is rejected with 413 before it's fully read into
// memory, not accepted and processed.
```

In `internal/adapter/driver/http/search_handler_test.go`, replace lines 93-96:

```go
// TestSearchHandler_RejectsOversizedBody proves the fix for the unbounded
// request-body finding: a /search body larger than
// constants.MaxSearchBodyBytes must be rejected with 413 before it's fully
// read into memory and bound.
```

with:

```go
// TestSearchHandler_RejectsOversizedBody asserts that a /search body larger
// than constants.MaxSearchBodyBytes is rejected with 413 before it's fully
// read into memory and bound.
```

- [ ] **Step 6: Consolidate the 3x-repeated htmx-swap comment into one package doc comment**

Create `internal/adapter/driver/web/doc.go`:

```go
// Package web is the server-rendered Go-template + htmx driver adapter for
// the same use cases internal/adapter/driver/http exposes as JSON — a
// second, HTML-rendering front end, not a second copy of the use cases.
//
// htmx fragment handlers (the "*_rows"/"*_results" endpoints the page's
// Refresh/submit buttons hx-get or hx-post) always respond 200, even on a
// use-case error: htmx does not swap a non-2xx response into its target by
// default, so a 404/500 response from a fragment endpoint would leave that
// target empty with the error invisible to the user. These handlers render
// the error message and slug inline inside the fragment instead, at 200.
// Full-page handlers (the "*_page" endpoints reached by navigation) have no
// such constraint and go through the normal renderError path (404 for
// domain.ErrNotFound, 500 otherwise).
package web
```

In `internal/adapter/driver/web/batch.go`, replace lines 45-51:

```go
// NewBatchRowsHandler renders just the status table, for the batch page's
// Refresh button to swap in via htmx. There is no automatic polling — the
// button fires this on click only.
//
// A use-case error here renders inline inside the fragment at 200, not
// through renderError: htmx does not swap a non-2xx response into its
// target by default, so a 404/500 full-page response from this endpoint
// would leave the Refresh button's target empty with the error invisible
// to the user. Rendering the error inside "batch_rows" at 200 keeps it
// visible.
```

with:

```go
// NewBatchRowsHandler renders just the status table, for the batch page's
// Refresh button to swap in via htmx. There is no automatic polling — the
// button fires this on click only. See the package doc comment for why a
// use-case error here renders inline at 200 instead of through renderError.
```

In `internal/adapter/driver/web/processing.go`, replace lines 65-71:

```go
// NewProcessingRowsHandler renders just the batch table, for the
// processing page's Refresh button to swap in via htmx. There is no
// automatic polling, matching the batch page's Refresh button.
//
// A use-case error here renders inline inside the fragment at 200, not
// through renderError: htmx does not swap a non-2xx response into its
// target by default, so a 404/500 full-page response from this endpoint
// would leave the Refresh button's target empty with the error invisible
// to the user. Rendering the error inside "processing_rows" at 200 keeps
// it visible — identical reasoning to NewBatchRowsHandler.
```

with:

```go
// NewProcessingRowsHandler renders just the batch table, for the
// processing page's Refresh button to swap in via htmx. There is no
// automatic polling, matching the batch page's Refresh button. See the
// package doc comment for why a use-case error here renders inline at 200
// instead of through renderError.
```

In `internal/adapter/driver/web/search.go`, replace lines 78-83:

```go
			resp, err := uc.Run(c.Request.Context(), req)
			if err != nil {
				// Renders inline inside the fragment at 200, not through
				// renderError: htmx does not swap a non-2xx response into its
				// target by default, so a 500 full-page response here would
				// leave #results empty with the error invisible to the user.
				_, slug, message := classifyError(c.Request.Context(), err)
```

with:

```go
			resp, err := uc.Run(c.Request.Context(), req)
			if err != nil {
				// Renders inline at 200 — see the package doc comment.
				_, slug, message := classifyError(c.Request.Context(), err)
```

- [ ] **Step 7: Verify and commit**

Run: `GOTOOLCHAIN=auto go build ./... && GOTOOLCHAIN=auto go vet ./... && GOTOOLCHAIN=auto go test ./... 2>&1 | tail -20`
Expected: build and vet succeed with no new warnings; every package still reports `ok` (no test assertions changed, only comments).

```bash
git add internal/constants/constants.go internal/adapter/driven/pdf/extractor.go internal/dto/dto.go internal/adapter/driver/multipartform/parse.go internal/adapter/driver/web/processing_test.go internal/adapter/driver/http/upload_handler_test.go internal/adapter/driver/http/search_handler_test.go internal/adapter/driver/web/batch.go internal/adapter/driver/web/processing.go internal/adapter/driver/web/search.go internal/adapter/driver/web/doc.go
git commit -m "docs: remove stale/repeated comments (removed constant refs, dead history, 3x-repeated htmx rationale)"
```

---

### Task 2: `constants.go` forward-reference fix

**Files:**
- Modify: `internal/constants/constants.go:27-44` and `:149-154`

**Interfaces:** None — pure reordering within one `const` block, values unchanged.

- [ ] **Step 1: Confirm the baseline is green**

Run: `GOTOOLCHAIN=auto go build ./... && GOTOOLCHAIN=auto go test ./internal/... 2>&1 | tail -10`
Expected: `ok` for every package.

- [ ] **Step 2: Move `ClassifyStageTimeout` to sit after `MaxExtractionRetries`**

In `internal/constants/constants.go`, remove this block (currently lines 34-37, between `ExtractStageTimeout` and `StatusWriteTimeout`):

```go
	// ClassifyStageTimeout bounds the classify stage's consumer: 3
	// independent LLM attempts (see MaxExtractionRetries, LLMAttemptTimeout)
	// plus slack for the surrounding save/publish work.
	ClassifyStageTimeout = time.Duration(MaxExtractionRetries)*LLMAttemptTimeout + 30*time.Second

```

so that section reads:

```go
	// ExtractStageTimeout bounds the extract stage's consumer: pdftotext's
	// fast path is near-instant, and the OCR fallback's measured worst case
	// is ~6.5s (5 pages × ~1.3s/page, see decisions.md) — 30s leaves ample
	// margin without letting a wedged pdftoppm/tesseract subprocess block
	// the consumer forever.
	ExtractStageTimeout = 30 * time.Second

	// StatusWriteTimeout bounds writeStatus's own UpdateStatus call. It is
```

Then insert the moved block immediately after `MaxExtractionRetries`'s definition (currently lines 149-154):

```go
	// MaxExtractionRetries bounds retries when the LLM returns invalid JSON
	// for structured field extraction (small/local models are the highest-risk
	// component for schema adherence). Retries only fire on failure, so this
	// also bounds worst-case per-resume token spend against a billed hosted
	// API to 3x one call's cost.
	MaxExtractionRetries = 3

	// ClassifyStageTimeout bounds the classify stage's consumer: 3
	// independent LLM attempts (see MaxExtractionRetries, LLMAttemptTimeout
	// above) plus slack for the surrounding save/publish work.
	ClassifyStageTimeout = time.Duration(MaxExtractionRetries)*LLMAttemptTimeout + 30*time.Second

```

(Both constants keep their exact existing values and doc-comment content — only `ClassifyStageTimeout`'s position changes, and its comment's "see MaxExtractionRetries... below" becomes "...above" since it's now defined after, not before.)

- [ ] **Step 3: Verify and commit**

Run: `GOTOOLCHAIN=auto go build ./... && GOTOOLCHAIN=auto go test ./... 2>&1 | tail -20`
Expected: build succeeds, every package still `ok` — this is a pure reorder, no value changed.

```bash
git add internal/constants/constants.go
git commit -m "refactor: move ClassifyStageTimeout next to MaxExtractionRetries, its only input"
```

---

### Task 3: Unexport `postgres.Connect`/`RunMigrations`

**Files:**
- Modify: `internal/adapter/driven/postgres/connect.go`
- Modify: `internal/adapter/driven/postgres/migrate.go`

**Interfaces:**
- Produces: `postgres.connect(ctx, connString) (*pgxpool.Pool, error)` (was `Connect`), `postgres.runMigrations(connString, dir) error` (was `RunMigrations`) — both now unexported, both still called only from `MigrateAndConnect` in the same package.

- [ ] **Step 1: Confirm no external callers exist**

Run: `grep -rn "postgres\.Connect\b\|postgres\.RunMigrations\b" --include="*.go" .`
Expected: no output (already confirmed during planning — this step re-verifies at implementation time in case something changed).

- [ ] **Step 2: Rename `Connect` to `connect`**

In `internal/adapter/driven/postgres/connect.go`, change:

```go
// Connect opens a pgxpool.Pool with the pgvector type codec registered on
// every new connection. Without this, binding/scanning pgvector.Vector
// against a VECTOR column fails at runtime — every caller (serve, worker,
// tests) must go through this constructor rather than pgxpool.New directly.
func Connect(ctx context.Context, connString string) (*pgxpool.Pool, error) {
```

to:

```go
// connect opens a pgxpool.Pool with the pgvector type codec registered on
// every new connection. Without this, binding/scanning pgvector.Vector
// against a VECTOR column fails at runtime — every caller (serve, worker,
// tests) must go through MigrateAndConnect rather than pgxpool.New directly.
func connect(ctx context.Context, connString string) (*pgxpool.Pool, error) {
```

- [ ] **Step 3: Rename `RunMigrations` to `runMigrations`, update both call sites**

In `internal/adapter/driven/postgres/migrate.go`, change:

```go
func MigrateAndConnect(ctx context.Context, connString, dir string) (*pgxpool.Pool, error) {
	if err := RunMigrations(connString, dir); err != nil {
		return nil, err
	}
	return Connect(ctx, connString)
}

// RunMigrations applies every migration in dir, in version order, via
// golang-migrate's pgx v5 driver.
//
// `app` and `worker` both call this on startup, so concurrent runs against
// the same fresh database are expected — e.g. `CREATE EXTENSION IF NOT
// EXISTS` is not safe under concurrent execution, since two sessions can
// both see "extension does not exist yet" and race on Postgres's
// pg_extension unique index. golang-migrate's Postgres driver takes its
// own pg_advisory_lock for the duration of a run, so this races safely
// without any locking code of our own — see
// TestMigrateAndConnect_ConcurrentCallersDoNotRace.
func RunMigrations(connString, dir string) error {
```

to:

```go
func MigrateAndConnect(ctx context.Context, connString, dir string) (*pgxpool.Pool, error) {
	if err := runMigrations(connString, dir); err != nil {
		return nil, err
	}
	return connect(ctx, connString)
}

// runMigrations applies every migration in dir, in version order, via
// golang-migrate's pgx v5 driver.
//
// `app` and `worker` both call this on startup, so concurrent runs against
// the same fresh database are expected — e.g. `CREATE EXTENSION IF NOT
// EXISTS` is not safe under concurrent execution, since two sessions can
// both see "extension does not exist yet" and race on Postgres's
// pg_extension unique index. golang-migrate's Postgres driver takes its
// own pg_advisory_lock for the duration of a run, so this races safely
// without any locking code of our own — see
// TestMigrateAndConnect_ConcurrentCallersDoNotRace.
func runMigrations(connString, dir string) error {
```

- [ ] **Step 4: Verify and commit**

Run: `GOTOOLCHAIN=auto go build ./...`
Expected: succeeds — if any other file referenced `postgres.Connect`/`postgres.RunMigrations`, this fails with an undefined-symbol error, which Step 1's grep should already have ruled out.

Run: `GOTOOLCHAIN=auto go test ./internal/adapter/driven/postgres/... 2>&1 | tail -10`
Expected: `ok` for unit tests. If a live Postgres stack is available, also run `make test-integration` and expect the existing `TestMigrateAndConnect_ConcurrentCallersDoNotRace` (and other postgres integration tests) to still pass unmodified — the rename doesn't change any behavior they observe. If no live stack is available, state that explicitly rather than claiming this was verified.

```bash
git add internal/adapter/driven/postgres/connect.go internal/adapter/driven/postgres/migrate.go
git commit -m "refactor: unexport postgres.Connect/RunMigrations, each has exactly one in-package caller"
```

---

### Task 4: Move `isTerminal` from `service` to `domain`

**Files:**
- Modify: `internal/domain/resume.go`
- Create: `internal/domain/resume_test.go`
- Modify: `internal/service/status_write.go`
- Modify: `internal/service/status_write_test.go`
- Modify: `internal/service/extract_resume.go:34`
- Modify: `internal/service/classify_resume.go:32`
- Modify: `internal/service/embed_resume.go:42`

**Interfaces:**
- Produces: `domain.Status.IsTerminal() bool` (method), replacing `service.isTerminal(status domain.Status) bool` (function, deleted).

- [ ] **Step 1: Confirm the baseline is green**

Run: `GOTOOLCHAIN=auto go test ./internal/domain/... ./internal/service/... 2>&1 | tail -10`
Expected: `ok` for both packages.

- [ ] **Step 2: Add `IsTerminal` to `domain.Status`, with its test**

In `internal/domain/resume.go`, after the `Status` const block (currently lines 19-26), add:

```go

// IsTerminal reports whether s is a final state that should make a
// pipeline stage's Run skip reprocessing on redelivery. This must be
// checked on Resume.Status, never Resume.Stage: the embed stage in
// particular has no AdvanceStage call after it (its terminal write is
// writeStatus(DONE)), so a crash between SaveChunks and that write leaves a
// row at stage=EMBED/status=PROCESSING — a state the sweeper is meant to
// redrive, not a state a stage-based guard would mistake for "already done."
func (s Status) IsTerminal() bool {
	return s == StatusDone || s == StatusFailed
}
```

Create `internal/domain/resume_test.go`:

```go
package domain_test

import (
	"testing"

	"resumesearch/internal/domain"
)

func TestStatus_IsTerminal(t *testing.T) {
	cases := []struct {
		name   string
		status domain.Status
		want   bool
	}{
		{"pending is not terminal", domain.StatusPending, false},
		{"processing is not terminal", domain.StatusProcessing, false},
		{"done is terminal", domain.StatusDone, true},
		{"failed is terminal", domain.StatusFailed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.status.IsTerminal(); got != tc.want {
				t.Errorf("%s.IsTerminal() = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run the new test to confirm it passes**

Run: `GOTOOLCHAIN=auto go test ./internal/domain/... -run TestStatus_IsTerminal -v`
Expected: `PASS`, all 4 subtests pass.

- [ ] **Step 4: Remove `isTerminal` and its test from `service`**

In `internal/service/status_write.go`, delete lines 47-56:

```go

// isTerminal reports whether status is a final state that should make a
// stage's Run skip reprocessing on redelivery. This must be checked on
// Resume.Status, never Resume.Stage: the embed stage in particular has no
// AdvanceStage call after it (its terminal write is writeStatus(DONE)), so
// a crash between SaveChunks and that write leaves a row at
// stage=EMBED/status=PROCESSING — a state the sweeper is meant to redrive,
// not a state a stage-based guard would mistake for "already done."
func isTerminal(status domain.Status) bool {
	return status == domain.StatusDone || status == domain.StatusFailed
}
```

In `internal/service/status_write_test.go`, delete lines 199-217 (the `TestIsTerminal` function and its preceding blank line):

```go

func TestIsTerminal(t *testing.T) {
	cases := []struct {
		name   string
		status domain.Status
		want   bool
	}{
		{"pending is not terminal", domain.StatusPending, false},
		{"processing is not terminal", domain.StatusProcessing, false},
		{"done is terminal", domain.StatusDone, true},
		{"failed is terminal", domain.StatusFailed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTerminal(tc.status); got != tc.want {
				t.Errorf("isTerminal(%s) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 5: Update the three call sites**

In `internal/service/extract_resume.go`, change line 34 from:

```go
	if isTerminal(resume.Status) {
```

to:

```go
	if resume.Status.IsTerminal() {
```

In `internal/service/classify_resume.go`, change line 32 the same way:

```go
	if isTerminal(resume.Status) {
```

to:

```go
	if resume.Status.IsTerminal() {
```

In `internal/service/embed_resume.go`, change line 42 the same way:

```go
	if isTerminal(resume.Status) {
```

to:

```go
	if resume.Status.IsTerminal() {
```

- [ ] **Step 6: Verify and commit**

Run: `GOTOOLCHAIN=auto go build ./... && GOTOOLCHAIN=auto go test ./... 2>&1 | tail -30`
Expected: build succeeds (confirms no remaining reference to the deleted `isTerminal` function); every package still `ok`, including `extract_resume_test.go`/`classify_resume_test.go`/`embed_resume_test.go`'s existing terminal-skip test cases, unmodified.

```bash
git add internal/domain/resume.go internal/domain/resume_test.go internal/service/status_write.go internal/service/status_write_test.go internal/service/extract_resume.go internal/service/classify_resume.go internal/service/embed_resume.go
git commit -m "refactor: move isTerminal from service to domain.Status.IsTerminal, its natural home"
```

---

### Task 5: Unify duplicate driver-layer interfaces

**Files:**
- Modify: `internal/service/upload_resumes.go`
- Modify: `internal/service/search_resumes.go`
- Modify: `internal/service/get_status.go`
- Modify: `internal/adapter/driver/http/upload_handler.go`
- Modify: `internal/adapter/driver/http/search_handler.go`
- Modify: `internal/adapter/driver/http/status_handler.go`
- Modify: `internal/adapter/driver/web/upload.go`
- Modify: `internal/adapter/driver/web/search.go`
- Modify: `internal/adapter/driver/web/batch.go`
- Modify: `internal/adapter/driver/web/web.go`

**Interfaces:**
- Produces: `service.UploadRunner`, `service.SearchRunner`, `service.BatchStatusReader` — three new exported interfaces in `service`, each satisfied without any change by an existing concrete use case (`*UploadResumesUseCase`, `*SearchResumesUseCase`, `*GetStatusUseCase` respectively).
- Consumes (unchanged): `service.UploadFile`, `dto.UploadBatchResponse`, `dto.SearchRequest`, `dto.SearchResponse`, `dto.BatchStatusResponse` — all pre-existing types, no changes.

Note for the implementer: this task removes duplicate *declarations* of the same interface shape across the `http` and `web` packages. It does **not** change `cmd/app/serve.go:58`'s call to `webdriver.New(router, uploadUC, statusUC, statusUC, searchUC)` — that line still passes `statusUC` twice, because `web.New` has a separate `batchListRunner` parameter (for `ListBatches`, used only by the processing-tab feature, with no `http`-side twin) that this task does not touch. Fixing *that* would mean merging `BatchStatusReader` and `batchListRunner` into one interface in `web.New`'s signature — a different, unscoped change. Don't do it as part of this task.

- [ ] **Step 1: Confirm the baseline is green**

Run: `GOTOOLCHAIN=auto go build ./... && GOTOOLCHAIN=auto go test ./internal/adapter/driver/... 2>&1 | tail -20`
Expected: `ok` for `http` and `web` packages.

- [ ] **Step 2: Add `UploadRunner` to `service`**

In `internal/service/upload_resumes.go`, after the `UploadFile` struct (currently lines 16-19), add:

```go

// UploadRunner is the seam driver adapters need to call UploadResumesUseCase
// without depending on its concrete type or its ResumeRepository/
// EventPublisher dependencies — satisfied by *UploadResumesUseCase.
type UploadRunner interface {
	Run(ctx context.Context, files []UploadFile) (dto.UploadBatchResponse, error)
}
```

- [ ] **Step 3: Add `SearchRunner` to `service`**

In `internal/service/search_resumes.go`, after `NewSearchResumesUseCase` (currently lines 18-20), add:

```go

// SearchRunner is the seam driver adapters need to call SearchResumesUseCase
// without depending on its concrete type — satisfied by
// *SearchResumesUseCase.
type SearchRunner interface {
	Run(ctx context.Context, req dto.SearchRequest) (dto.SearchResponse, error)
}
```

- [ ] **Step 4: Add `BatchStatusReader` to `service`**

In `internal/service/get_status.go`, after `NewGetStatusUseCase` (currently lines 15-17), add:

```go

// BatchStatusReader is the seam driver adapters need to look up one batch's
// status without depending on GetStatusUseCase's concrete type — satisfied
// by *GetStatusUseCase.
type BatchStatusReader interface {
	ByBatchID(ctx context.Context, batchID string) (dto.BatchStatusResponse, error)
}
```

- [ ] **Step 5: Point `http`'s three handlers at the shared interfaces**

In `internal/adapter/driver/http/upload_handler.go`, replace lines 15-20:

```go
// uploadRunner is the minimal seam the handler needs — satisfied by
// *service.UploadResumesUseCase, but expressed as an interface here so
// the handler is testable without the full use case.
type uploadRunner interface {
	Run(ctx context.Context, files []service.UploadFile) (dto.UploadBatchResponse, error)
}

func NewUploadHandler(uc uploadRunner) gin.HandlerFunc {
```

with:

```go
func NewUploadHandler(uc service.UploadRunner) gin.HandlerFunc {
```

(`dto` import in this file becomes unused by this change alone — leave it; `dto.UploadBatchResponse`/`dto.ResumeRef` are still referenced inside the handler body at lines 41/28 respectively. Confirm with `go build` in Step 8; if `dto` or `context` end up genuinely unused after this edit, remove the import, but based on the current file body they are not.)

In `internal/adapter/driver/http/search_handler.go`, replace lines 14-16:

```go
type searchRunner interface {
	Run(ctx context.Context, req dto.SearchRequest) (dto.SearchResponse, error)
}

func NewSearchHandler(uc searchRunner) gin.HandlerFunc {
```

with:

```go
func NewSearchHandler(uc service.SearchRunner) gin.HandlerFunc {
```

This file's imports need `"resumesearch/internal/service"` added (currently imports `constants` and `dto` only) — add it to the import block:

```go
import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/constants"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)
```

(`context` was only used by the deleted `searchRunner` interface — check whether `context` is still used elsewhere in this file; if not, remove it from the import block. Verify in Step 8's build.)

In `internal/adapter/driver/http/status_handler.go`, replace lines 18-20:

```go
type statusByBatchRunner interface {
	ByBatchID(ctx context.Context, batchID string) (dto.BatchStatusResponse, error)
}
```

with nothing (delete these three lines and the blank line that follows them) — then change:

```go
func NewBatchStatusHandler(uc statusByBatchRunner) gin.HandlerFunc {
```

to:

```go
func NewBatchStatusHandler(uc service.BatchStatusReader) gin.HandlerFunc {
```

Add `"resumesearch/internal/service"` to this file's imports:

```go
import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)
```

(`statusByIDRunner`, used by `NewStatusHandler`, is untouched — it has no `web`-side twin, so it stays exactly as-is.)

- [ ] **Step 6: Point `web`'s handlers at the shared interfaces**

In `internal/adapter/driver/web/upload.go`, replace lines 14-18:

```go
// uploadRunner is the seam the upload handlers need — satisfied by
// *service.UploadResumesUseCase.
type uploadRunner interface {
	Run(ctx context.Context, files []service.UploadFile) (dto.UploadBatchResponse, error)
}
```

with nothing (delete these five lines, including the blank line before `// NewUploadPageHandler`), then change:

```go
func NewUploadSubmitHandler(uc uploadRunner) gin.HandlerFunc {
```

to:

```go
func NewUploadSubmitHandler(uc service.UploadRunner) gin.HandlerFunc {
```

(`context` and `dto` were used by the deleted interface and remain used elsewhere in the file's handler bodies — no import changes needed here; verify in Step 8.)

In `internal/adapter/driver/web/search.go`, replace lines 14-18:

```go
// searchRunner is the seam the search handlers need — satisfied by
// *service.SearchResumesUseCase.
type searchRunner interface {
	Run(ctx context.Context, req dto.SearchRequest) (dto.SearchResponse, error)
}
```

with nothing (delete these five lines including the blank line before the `searchResultsView` doc comment), then change:

```go
func NewSearchSubmitHandler(uc searchRunner) gin.HandlerFunc {
```

to:

```go
func NewSearchSubmitHandler(uc service.SearchRunner) gin.HandlerFunc {
```

Add `"resumesearch/internal/service"` to this file's imports:

```go
import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)
```

(`context` was only used by the deleted `searchRunner` interface — remove it from the import block if `go build` in Step 8 flags it as unused; based on the current file body, `context` is not used elsewhere in this file, so remove it now.)

In `internal/adapter/driver/web/batch.go`, replace lines 12-16:

```go
// batchStatusRunner is the seam the batch handlers need — satisfied by
// *service.GetStatusUseCase.
type batchStatusRunner interface {
	ByBatchID(ctx context.Context, batchID string) (dto.BatchStatusResponse, error)
}
```

with nothing (delete these five lines including the blank line before `// batchRowsView`), then change both:

```go
func NewBatchPageHandler(uc batchStatusRunner) gin.HandlerFunc {
```

and

```go
func NewBatchRowsHandler(uc batchStatusRunner) gin.HandlerFunc {
```

to:

```go
func NewBatchPageHandler(uc service.BatchStatusReader) gin.HandlerFunc {
```

and

```go
func NewBatchRowsHandler(uc service.BatchStatusReader) gin.HandlerFunc {
```

respectively. Add `"resumesearch/internal/service"` to this file's imports; remove `"context"` if it becomes unused (based on the current file body, `context` was only used by the deleted interface, so remove it):

```go
import (
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)
```

- [ ] **Step 7: Update `web.New`'s parameter types**

In `internal/adapter/driver/web/web.go`, change:

```go
func New(router *gin.Engine, uploadUC uploadRunner, statusUC batchStatusRunner, listUC batchListRunner, searchUC searchRunner) {
```

to:

```go
func New(router *gin.Engine, uploadUC service.UploadRunner, statusUC service.BatchStatusReader, listUC batchListRunner, searchUC service.SearchRunner) {
```

Add `"resumesearch/internal/service"` to this file's imports:

```go
import (
	"github.com/gin-gonic/gin"

	"resumesearch/internal/service"
)
```

(`batchListRunner` stays exactly as declared in `processing.go` — it has no `http`-side twin, so it's correctly untouched by this task.)

- [ ] **Step 8: Verify and commit**

Run: `GOTOOLCHAIN=auto go build ./... 2>&1`
Expected: succeeds. If it reports an unused import, remove exactly that import and re-run — do not remove any import the build doesn't flag.

Run: `GOTOOLCHAIN=auto go test ./... 2>&1 | tail -30`
Expected: every package still `ok`. In particular, `http` and `web` packages' existing handler tests (`upload_handler_test.go`, `search_handler_test.go`, `status_handler_test.go`, `upload_test.go`, `search_test.go`, `batch_test.go`) must pass unmodified — they construct handlers via `stubUploadRunner`/`stubSearchRunner`/`stubBatchStatusRunner` test doubles, which still satisfy the new shared interfaces (`service.UploadRunner` etc.) with no changes, since the method signatures are unchanged.

```bash
git add internal/service/upload_resumes.go internal/service/search_resumes.go internal/service/get_status.go internal/adapter/driver/http/upload_handler.go internal/adapter/driver/http/search_handler.go internal/adapter/driver/http/status_handler.go internal/adapter/driver/web/upload.go internal/adapter/driver/web/search.go internal/adapter/driver/web/batch.go internal/adapter/driver/web/web.go
git commit -m "refactor: declare UploadRunner/SearchRunner/BatchStatusReader once in service, remove 4 duplicate declarations across http and web"
```

---

### Task 6: Shared stage-preamble helper for extract/classify/embed

**Files:**
- Modify: `internal/service/status_write.go`
- Modify: `internal/service/extract_resume.go`
- Modify: `internal/service/classify_resume.go`
- Modify: `internal/service/embed_resume.go`

**Interfaces:**
- Produces: `beginStage(ctx context.Context, repo ResumeRepository, resumeID string) (domain.Resume, bool, error)` — unexported helper in `service`, used only by the three `Run` methods below.
- Consumes: `domain.Resume`, `domain.Status.IsTerminal()` (from Task 4), `ResumeRepository.GetByID`/`UpdateStatus` (unchanged, pre-existing).

- [ ] **Step 1: Confirm the baseline is green**

Run: `GOTOOLCHAIN=auto go test ./internal/service/... -v -run 'TestExtractResumeUseCase|TestClassifyResumeUseCase|TestEmbedResumeUseCase' 2>&1 | tail -40`
Expected: all three use cases' existing table-driven tests `PASS`.

- [ ] **Step 2: Add `beginStage` to `status_write.go`**

In `internal/service/status_write.go`, after `writeStatus` (currently ending at line 29) and before `failResume`, add:

```go

// beginStage loads resumeID and marks it Processing, unless it's already
// in a terminal state — in which case the caller should return nil and
// skip its stage-specific work, treating this as a no-op success. Every
// pipeline stage (extract/classify/embed) starts its Run this way; this
// exists so that identical preamble isn't hand-rolled three times with the
// same error-wrap strings and the same terminal-skip logic.
func beginStage(ctx context.Context, repo ResumeRepository, resumeID string) (domain.Resume, bool, error) {
	resume, err := repo.GetByID(ctx, resumeID)
	if err != nil {
		return domain.Resume{}, false, fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if resume.Status.IsTerminal() {
		return resume, false, nil
	}
	if err := repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return domain.Resume{}, false, fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}
	return resume, true, nil
}
```

- [ ] **Step 3: Use `beginStage` in `extract_resume.go`**

In `internal/service/extract_resume.go`, replace lines 29-40:

```go
func (uc *ExtractResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, err := uc.repo.GetByID(ctx, resumeID)
	if err != nil {
		return fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if resume.Status.IsTerminal() {
		return nil
	}

	if err := uc.repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}

	rawText, err := uc.extractor.ExtractText(ctx, resume.FilePath)
```

with:

```go
func (uc *ExtractResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, proceed, err := beginStage(ctx, uc.repo, resumeID)
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	rawText, err := uc.extractor.ExtractText(ctx, resume.FilePath)
```

(This edit assumes Task 4 already changed `isTerminal(resume.Status)` to `resume.Status.IsTerminal()` in this file — if Task 4 hasn't run yet, do it first; the two tasks must land in the order given in the plan.)

- [ ] **Step 4: Use `beginStage` in `classify_resume.go`**

In `internal/service/classify_resume.go`, replace lines 27-38:

```go
func (uc *ClassifyResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, err := uc.repo.GetByID(ctx, resumeID)
	if err != nil {
		return fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if resume.Status.IsTerminal() {
		return nil
	}

	if err := uc.repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}

	fields, err := uc.model.Extract(ctx, resume.RawText)
```

with:

```go
func (uc *ClassifyResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, proceed, err := beginStage(ctx, uc.repo, resumeID)
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	fields, err := uc.model.Extract(ctx, resume.RawText)
```

- [ ] **Step 5: Use `beginStage` in `embed_resume.go`**

In `internal/service/embed_resume.go`, replace lines 37-48:

```go
func (uc *EmbedResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, err := uc.repo.GetByID(ctx, resumeID)
	if err != nil {
		return fmt.Errorf("get resume %s: %w", resumeID, err)
	}
	if resume.Status.IsTerminal() {
		return nil
	}

	if err := uc.repo.UpdateStatus(ctx, resumeID, domain.StatusProcessing, ""); err != nil {
		return fmt.Errorf("mark resume %s processing: %w", resumeID, err)
	}

	textChunks := utils.RecursiveSplit(resume.RawText, constants.ChunkSizeWords)
```

with:

```go
func (uc *EmbedResumeUseCase) Run(ctx context.Context, resumeID string) error {
	resume, proceed, err := beginStage(ctx, uc.repo, resumeID)
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}

	textChunks := utils.RecursiveSplit(resume.RawText, constants.ChunkSizeWords)
```

The doc comment directly above `Run` (currently lines 25-36) explaining why the terminal guard is load-bearing for this stage specifically must stay — it's about *why* `IsTerminal`/`beginStage`'s skip matters here, not about the preamble's mechanics, so it still belongs at this call site. Leave it exactly as-is; only the function body inside `Run` changes.

- [ ] **Step 6: Check for now-unused imports**

`extract_resume.go` and `classify_resume.go` each still use `domain` for other purposes (`domain.StageClassify`/`domain.StageEmbed` in `AdvanceStage` calls, `domain.ExtractedFields` implicitly via `uc.model.Extract`'s return type in classify) — check with the build in Step 7 rather than removing anything preemptively. `fmt` remains used in all three files (error-wrapping in the stage-specific work below the preamble).

- [ ] **Step 7: Verify and commit**

Run: `GOTOOLCHAIN=auto go build ./... 2>&1`
Expected: succeeds. Fix any unused-import error by removing exactly the flagged import.

Run: `GOTOOLCHAIN=auto go test ./internal/service/... -v -run 'TestExtractResumeUseCase|TestClassifyResumeUseCase|TestEmbedResumeUseCase' 2>&1 | tail -60`
Expected: every existing subtest still `PASS` with no assertion changes — same `wantStatuses` sequences (e.g. `[StatusProcessing, StatusFailed]`), same `wantErr` outcomes, since `beginStage` makes the exact same `GetByID`/`UpdateStatus` calls in the exact same order the inlined code made before.

Run: `GOTOOLCHAIN=auto go test ./... 2>&1 | tail -20`
Expected: every package `ok`.

```bash
git add internal/service/status_write.go internal/service/extract_resume.go internal/service/classify_resume.go internal/service/embed_resume.go
git commit -m "refactor: extract beginStage helper, remove identical load+guard+mark-processing boilerplate from extract/classify/embed"
```

---

## Self-Review Notes

- **Spec coverage:** all 6 spec items have a corresponding task (Task 1 → spec item 1, Task 2 → item 2, Task 3 → item 3, Task 4 → item 4, Task 5 → item 5, Task 6 → item 6). No spec item without a task; no task outside the spec's 6 items.
- **Correction from the brainstorming discussion:** the design conversation stated that unifying the duplicate driver interfaces (Task 5) would let `cmd/app/serve.go:58` "pass `statusUC` once instead of twice." On re-reading `web.go` while writing this plan, that's inaccurate — `web.New` has a separate `batchListRunner` parameter (for the processing tab's `ListBatches`, with no `http`-side twin) that Task 5 doesn't touch, so `serve.go:58` still passes `statusUC` twice after this task. Task 5's note under **Interfaces** above states the corrected scope explicitly so the implementer doesn't chase a win that isn't there.
- **Ordering dependency:** Task 6 depends on Task 4 (it calls `resume.Status.IsTerminal()`, which Task 4 introduces) — Step 3 of Task 6 flags this explicitly. Tasks 1-5 have no cross-task dependencies and could theoretically run in a different order, but the plan's given order (ascending risk) should still be followed.
- **Type consistency check:** `beginStage`'s signature (`(domain.Resume, bool, error)`) is used identically across Tasks 6's Steps 3-5 — same three-value destructuring (`resume, proceed, err`) in all three call sites. `service.UploadRunner`/`SearchRunner`/`BatchStatusReader` (Task 5) match the exact pre-existing method signatures of `*UploadResumesUseCase.Run`, `*SearchResumesUseCase.Run`, `*GetStatusUseCase.ByBatchID` respectively — confirmed against each use case's actual current source during planning, not assumed.
