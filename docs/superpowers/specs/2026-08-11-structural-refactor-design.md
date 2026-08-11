# Structural Cleanup Refactor — Design

## Context

The codebase is small (~9K lines including tests, ~4.5K non-test) but has accumulated organizational cruft from iterative feature work: stale comments referencing removed constants, duplicated interface declarations across driver packages that force awkward wiring, duplicated control-flow boilerplate across the three pipeline-stage use cases, and a couple of unnecessarily exported functions. None of this is a correctness bug — the goal is reducing cognitive load for a reviewer and removing loose ends, with **exact behavior preserved**. No new features, no production-hardening work (CI, secrets store, healthz, ANN indexes, correlation IDs, panic recovery, etc. — all already tracked separately in `docs/production-readiness.md` and explicitly out of scope here).

This followed a three-way codebase audit (driver layer, service layer, constants/repository/docs) that surfaced far more candidates than are in scope below. Two were deliberately excluded after review:
- **JSON API error responses leaking raw `err.Error()`** (`status_handler.go`, `search_handler.go`, `upload_handler.go`) — a real gap the web UI already fixed via `classifyError`, but fixing it changes response *body content* on error paths, which is a behavior change, not a refactor. Deferred as a separate, explicit security fix if wanted later.
- **Consolidating the three near-duplicate publisher ports** (`EventPublisher`/`ExtractedPublisher`/`ClassifiedPublisher`) and/or splitting the 11-method `ResumeRepository` port — real but the evidence for pain is mostly test-file size (`fakes_test.go`), not reviewer comprehension. Debatable win, not pursued here.

## Scope

Six independent, behavior-preserving changes. Each is small enough to verify in isolation; land as separate commits in the order below (ascending risk).

### 1. Stale/repeated comment cleanup

Pure comment edits and deletions — no logic touched, zero behavior risk.

- `internal/constants/constants.go:207-212` and `internal/adapter/driven/pdf/extractor.go:100`: both reference `ResumeProcessingTimeout`, a constant removed when the pipeline split into 3 Kafka stages (per `decisions.md`). Replace with a reference to the actual timeout that bounds the OCR path today (`ExtractStageTimeout`).
- `internal/dto/dto.go:75-81`: drop the aside about "unlike the old 'distance' field" — it narrates removed history, not current behavior.
- `internal/adapter/driver/multipartform/parse.go:29-32`: drop "a planned web driver adapter (a later task in this plan) will too" — that adapter (`internal/adapter/driver/web`) already exists and already calls this helper (`web/upload.go:36`). State the sharing as present fact or delete the aside.
- Test comments citing a commit hash instead of describing the behavior under test: `internal/adapter/driver/web/processing_test.go:100-102`, `internal/adapter/driver/http/upload_handler_test.go:126-129`, `internal/adapter/driver/http/search_handler_test.go:93-96`. Rewrite each to state the property being verified (e.g. "oversized request body returns 413, not 500") without the commit reference.
- The htmx-swap-behavior rationale ("htmx does not swap a non-2xx response into its target by default...") is currently repeated near-verbatim three times: `internal/adapter/driver/web/batch.go:45-51`, `processing.go:66-71`, `search.go:79-83`. State it once as a package-level doc comment (new `internal/adapter/driver/web/doc.go`, or on the `web` package's existing entry point in `web.go`), delete the other two copies, leave a one-line pointer at each of the three call sites if useful context is otherwise lost.

### 2. `constants.go` forward-reference

`ClassifyStageTimeout` (currently line 34-37) is defined as `time.Duration(MaxExtractionRetries)*LLMAttemptTimeout + 30*time.Second`, but `MaxExtractionRetries` isn't defined until line 152-158, in an unrelated comment group. Move `ClassifyStageTimeout`'s definition down to sit immediately after `MaxExtractionRetries` (derived value reads naturally after its inputs) rather than moving `MaxExtractionRetries` up — `LLMAttemptTimeout`'s existing comment group (lines 46-64) reads better staying intact where it is, next to the other per-attempt/timeout constants it's grouped with.

### 3. Unexport two single-caller functions

- `internal/adapter/driven/postgres/connect.go`: `Connect` → `connect`. Only caller is `MigrateAndConnect` in the same package (`migrate.go:24`).
- `internal/adapter/driven/postgres/migrate.go`: `RunMigrations` → `runMigrations`. Only caller is `MigrateAndConnect` in the same package (`migrate.go:21`).

Mechanical rename, update the two call sites, no test changes needed (neither is called from any `_test.go` file outside the package per the earlier audit).

### 4. Move `isTerminal` into `domain`

`internal/service/status_write.go:54-56`'s `isTerminal(status domain.Status) bool` is pure domain logic (a predicate over `Status` values) with a doc comment already written entirely in domain terms. Move it to `internal/domain/resume.go` as a method:

```go
func (s Status) IsTerminal() bool {
    return s == StatusDone || s == StatusFailed
}
```

(matches the current `isTerminal(status domain.Status) bool` body in `status_write.go:54-56` exactly — same two constants, same `||`). Update the three call sites (`extract_resume.go`, `classify_resume.go`, `embed_resume.go` — wherever `isTerminal(resume.Status)` is called today) to `resume.Status.IsTerminal()`. Move its existing unit test from `status_write_test.go` to a new `internal/domain/resume_test.go`.

### 5. Unify duplicate driver-layer interfaces

Four interfaces are declared twice — once in `internal/adapter/driver/http`, once in `internal/adapter/driver/web` — for the same method set, sometimes under different names:

- `uploadRunner` (`http/upload_handler.go:18-20`, `web/upload.go:16-18`) — identical.
- `searchRunner` (`http/search_handler.go:14-16`, `web/search.go:16-18`) — identical.
- `statusByBatchRunner` (`http/status_handler.go:18-20`) vs `batchStatusRunner` (`web/batch.go:14-16`) — same method (`ByBatchID(ctx, batchID) (dto.BatchStatusResponse, error)`) under two names. This split is why `cmd/app/serve.go:58` has to pass `statusUC` twice into `webdriver.New(...)`.
  **Correction (see docs/superpowers/plans/2026-08-11-structural-refactor.md's Self-Review Notes):** this last sentence is wrong — `serve.go:58` passes `statusUC` twice because `webdriver.New` has a separate `batchListRunner` parameter (for `ListBatches`, untouched by this item), not because of the interface-naming split described above. Unifying these interfaces does not remove either `statusUC` argument.

Declare each once, exported, in `internal/service` next to the concrete use case it describes (e.g. `service.UploadRunner`, `service.SearchRunner`, `service.BatchStatusReader`) — both driver packages already import `service` for the concrete use-case types, so this adds no new dependency edge. The existing concrete types (`*UploadResumesUseCase`, `*SearchResumesUseCase`, `*GetStatusUseCase`) already satisfy these shapes with no changes. Both `http` and `web` handler files switch their local interface declarations to references to `service.XxxRunner`; `cmd/app/serve.go:58` passes `statusUC` once instead of twice once `webdriver.New`'s signature is updated to match. **Correction (see docs/superpowers/plans/2026-08-11-structural-refactor.md's Self-Review Notes):** this is inaccurate — `webdriver.New`'s separate `batchListRunner` parameter is untouched by this item, so `serve.go:58` still passes `statusUC` twice after this change; the actual implementation correctly left `serve.go` unchanged.

Test stub types (`stubUploadRunner`, `stubSearchRunner`, `stubBatchStatusRunner` — currently duplicated per package) are unaffected by this change on their own; they still need one stub per package since Go doesn't let two packages share a private test type without exporting it, and that's not being changed here — flagged as a known remaining duplication, not part of this pass.

### 6. Shared stage-preamble helper for extract/classify/embed

All three pipeline-stage use cases (`extract_resume.go`, `classify_resume.go`, `embed_resume.go`) open with byte-for-byte-equivalent boilerplate: load the resume, skip if terminal, mark it `Processing`. Extract to a new function in `internal/service/status_write.go` (already home to the shared `writeStatus`/`failResume`):

```go
// beginStage loads resumeID and marks it Processing, unless it's already in
// a terminal state (in which case the caller should treat this as a no-op
// success — a resume that finished or failed shouldn't be reprocessed by a
// redelivered or redriven message).
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

Each `Run` method becomes:

```go
resume, proceed, err := beginStage(ctx, uc.repo, resumeID)
if err != nil {
    return err
}
if !proceed {
    return nil
}
// ...stage-specific work unchanged...
```

Error-wrap strings must stay byte-identical to what each of the three files produces today (confirmed identical across all three by the audit, so this is a pure extraction, not a behavior change). `embed_resume.go`'s existing doc comment explaining that its terminal guard is load-bearing for a different reason than extract/classify's stays at the embed call site — `beginStage` itself doesn't need to know why callers care, only that they do.

## Testing / Verification

- Every change here is covered by existing unit tests; none should require modifying test *assertions*, only import paths, call-site syntax (`isTerminal(x)` → `x.IsTerminal()`), or moving a test to a new file. If any existing test needs its assertions changed to pass, that's a signal behavior shifted — stop and re-examine.
- `go build ./...`, `go vet ./...`, `go test ./...` after every individual item, not just at the end — land each as its own commit per the sequencing below.
- Items 3, 4, and 6 touch `internal/service`/`internal/domain`/`internal/adapter/driven/postgres` — verify against integration tests too (`make test-integration`, needs a live Postgres/Kafka stack) since the unit suite alone doesn't exercise real `Repository` calls. If that stack isn't available when implementing, say so explicitly rather than claiming full verification.
- Item 5 changes `webdriver.New`'s signature (one fewer parameter) — update its call site in `cmd/app/serve.go:58` and confirm `go build ./...` catches any other caller (there is only the one). **Correction (see docs/superpowers/plans/2026-08-11-structural-refactor.md's Self-Review Notes):** the "one fewer parameter" / `serve.go:58` call-site-update premise is wrong for the same reason as above — `webdriver.New` keeps its `batchListRunner` parameter, so this item does not shrink its signature or change `serve.go`.

## Sequencing

Ascending risk, each its own commit:
1. Comment/staleness cleanup (item 1) — zero logic risk, establishes a clean baseline to verify the rest against.
2. `constants.go` reordering (item 2) — pure move, zero risk.
3. Unexport `connect`/`runMigrations` (item 3) — mechanical rename, zero risk.
4. Move `isTerminal` to `domain` (item 4) — small, mechanical, low risk.
5. Unify duplicate driver interfaces (item 5) — touches wiring in `serve.go`, low-medium risk (verify build catches the signature change).
6. Shared stage-preamble helper (item 6) — touches the three pipeline use cases directly; highest risk of this batch, land last so any regression is easy to isolate.
