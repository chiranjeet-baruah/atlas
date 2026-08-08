# Minimal Go Web UI — Design

## Context

`atlas` currently exposes a pure JSON API (Gin) for uploading resumes, polling ingestion status, and searching. There is no frontend. This design adds a minimal, server-rendered Go web UI for the four existing operations, with no JS build step, no frontend framework, and no new external runtime dependency (htmx is vendored into the binary).

## Architecture

A new driver adapter, `internal/adapter/driver/web`, sits alongside the existing `internal/adapter/driver/http` package. It is registered on the same Gin router in `cmd/app/serve.go`, under its own route namespace (`/ui/*`) to avoid colliding with the existing JSON routes (notably `POST /search`, which the web UI's `POST /ui/search` must not collide with).

The web handlers call the existing use cases directly — `uploadUC`, `statusUC`, `searchUC` (the same instances constructed in `serve.go` for the JSON API) — rather than making HTTP calls to the JSON API. This keeps the web adapter a thin driver, consistent with the hexagonal pattern already in the repo: adapters call use cases, use cases don't know about adapters.

Templates, a small stylesheet, and a vendored copy of `htmx.min.js` are embedded into the binary via `embed.FS`. Templates are loaded with `template.ParseFS` and registered via `router.SetHTMLTemplate` — not `router.LoadHTMLGlob`, which reads from disk and would require a Dockerfile change to `COPY` template files into the image. The embedded CSS and JS are served via `http.FS` over the embedded filesystem, so no static-file `COPY` is needed either.

Template naming convention: each page template defines a single top-level `{{define "name"}}` block; `c.HTML` calls resolve by that define name, not by filename. Fragment templates (used for htmx swaps) follow the same convention with their own define names.

## Pages & Routes

| Method | Path | Purpose |
|---|---|---|
| GET | `/ui/upload` | Upload form page |
| POST | `/ui/upload` | Parse multipart files, call `uploadUC.Upload`, 303-redirect to the batch page |
| GET | `/ui/batch/:batch_id` | Full page: batch status table + refresh button |
| GET | `/ui/batch/:batch_id/rows` | HTML fragment: just the status table body, for the refresh button to swap in |
| GET | `/ui/search` | Search form page |
| POST | `/ui/search` | Validate input, call `searchUC.Search`, render results fragment |

`POST /ui/upload` parses multipart files via the shared `internal/adapter/driver/multipartform.ParseUploadFiles` helper, which also backs the JSON API's upload handler — this is the one shared helper in the plan, extracted so `MaxUploadBytes`/`MaxUploadFiles` enforcement lives in exactly one place.

## Polling / Refresh

Status updates are manual, not automatic. The batch page renders a status table and a "Refresh" button. The button carries `hx-get="/ui/batch/:batch_id/rows"` and `hx-target`/`hx-swap="outerHTML"` pointing at the table body, so a click re-fetches and swaps in the current rows without a full page reload. There is no `hx-trigger="every ...s"` anywhere — no background polling loop.

## Data Flow

**Upload:** browser submits a multipart form to `POST /ui/upload` → handler parses files → calls `uploadUC.Upload` (same use case the JSON API uses) → on success, 303-redirects to `GET /ui/batch/:batch_id`; on a parse failure (zero files, oversized body, too many files), re-renders the upload form with an inline error, HTTP 200; on a use-case error, renders the generic error page via the same `renderError` path as the batch and search full-page handlers.

**Batch status:** `GET /ui/batch/:batch_id` and `GET /ui/batch/:batch_id/rows` both call `statusUC.GetBatchStatus` and render the same row data — the first as a full page, the second as just the table body fragment for the refresh button to swap in.

**Search:** form submits to `POST /ui/search` → handler validates input (empty query → inline error, no use-case call; non-numeric or empty `min_years` → treated as `nil`, never coerced to `0`) → calls `searchUC.Search` → renders a results table in the exact order the use case returns it. The `Distance` column is labeled "distance (lower = closer)" and is never client-sortable — `dto.go`'s existing comment on `SearchResultDTO.Distance` warns explicitly that re-sorting this field as a "score" would invert the ranking, since results already come back best-first from the repository.

## Error Handling

- Bad form input (empty query, non-numeric `min_years`, zero files on upload): re-render the originating form with an inline error message, HTTP 200.
- `domain.ErrNotFound` (unknown batch/resume id): simple "not found" page, HTTP 404.
- Any other use-case error: generic error page, HTTP 500, no internal error detail or stack trace rendered.

## Styling

A single small hand-written `style.css`, embedded alongside the templates, no CSS framework. Plain semantic HTML (forms, tables, buttons) — enough to be legible, nothing more.

## htmx

`htmx.min.js` is downloaded once and committed as a vendored static asset under the web adapter's embedded assets directory, served from the binary via `embed.FS` + `http.FS`. It is not loaded from a CDN — the app must not depend on outbound network access at runtime (`docker compose up` is expected to work offline once images/models are pulled).

## Testing

Table-driven handler tests per new file (`upload_page_test.go`, `batch_page_test.go`, `search_page_test.go`), following the existing fake-use-case pattern in `internal/service/fakes_test.go`. Tests assert HTTP status codes, `Location` header on redirects, and presence of key content in the rendered HTML (error messages, result rows, etc.) rather than doing full DOM comparison.

The Makefile's `test-unit` target hand-enumerates packages rather than using `./...`; `./internal/adapter/driver/web/...` must be added to that list, or its tests will silently never run in CI while `make test` still reports green.
