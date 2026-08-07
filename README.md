# atlas

High-performance PDF ingestion and indexing service for AI-powered knowledge retrieval and vector search.

Bulk-upload PDF resumes, process them asynchronously through a 3-stage Kafka pipeline (text extraction → LLM field extraction → chunked embeddings), and search them with a hybrid semantic + filtered query — all via `docker compose up`.

## Architecture

Hexagonal (ports & adapters): `domain` (framework-free entities) → `service` (use cases + port interfaces) → `adapter/driver` (HTTP via Gin, Kafka consumer — drive the app) / `adapter/driven` (Postgres, Kafka producer, Docker Model Runner client, PDF extraction — driven by the app). See [decisions.md](decisions.md) for the reasoning behind every non-obvious choice.

### Ingestion flow

Each resume moves through 3 independent Kafka topics/consumer groups — extract, classify, embed — so a slow LLM call can't block extraction or embedding for other resumes on the same stage. A `stage` column tracks which one a resume is currently on; `GET /resumes/:id` exposes it directly. A periodic redrive sweeper reclaims any resume that hasn't advanced in a while (crashed mid-stage, before its next publish) and republishes it to the right topic, up to a bounded number of retries before giving up and marking it `FAILED`.

```mermaid
flowchart LR
    C[Client] -->|POST /resumes/batch<br/>multipart, N files| A[app serve / Gin]
    A -->|write files| V[(shared volume)]
    A -->|INSERT status=PENDING<br/>stage=EXTRACT| DB[(Postgres)]
    A -->|publish resume_id| K1[[resume.ingest.requested]]
    K1 --> WE[extract worker]
    WE -->|pdftotext / OCR fallback| WE
    WE -->|save raw_text, stage=CLASSIFY| DB
    WE -->|publish resume_id| K2[[resume.text.extracted]]
    K2 --> WC[classify worker]
    WC -->|LLM extract fields,<br/>validate+retry| WC
    WC -->|save fields, stage=EMBED| DB
    WC -->|publish resume_id| K3[[resume.fields.classified]]
    K3 --> WB[embed worker]
    WB -->|chunk ~512 tokens, embed each| WB
    WB -->|UPSERT chunks, status=DONE/FAILED| DB
    SW[redrive sweeper] -.->|reclaim stale resume,<br/>republish to its stage's topic| K1
    SW -.-> K2
    SW -.-> K3
    DB -.->|scan for stale rows| SW
```

### Query flow

```mermaid
flowchart LR
    S[POST /search<br/>query + filters] --> E[Embed query via DMR]
    E --> Q[SQL: skills/years/location filters<br/>+ vector distance ORDER BY]
    Q --> DB[(Postgres)]
    DB --> R[Ranked JSON results]
```

## Running it

```bash
docker compose up --build
```

First run pulls the LLM/embedding models via Docker Model Runner (`ai/llama3.2` and `ai/nomic-embed-text-v1.5`) — expect that step to take a few minutes, and expect the very first extraction request afterward to pay an extra ~25s while the LLM loads into the runner (see `LLMAttemptTimeout` in `internal/constants/constants.go`).

## Try it with the sample resumes

```bash
curl -F "files=@data/resumes/01_alice.pdf" -F "files=@data/resumes/02_bruno.pdf" http://localhost:8080/resumes/batch
# -> {"batch_id": "...", "resumes": [{"id": "...", "filename": "01_alice.pdf"}, ...]}

curl http://localhost:8080/resumes/batch/<batch_id>
# poll until every resume shows "status": "DONE"
# ("stage" shows which of EXTRACT/CLASSIFY/EMBED a still-PROCESSING resume is on)

curl -X POST http://localhost:8080/search \
  -H "Content-Type: application/json" \
  -d '{"query": "backend engineer with distributed systems experience", "required_skills": ["go", "kafka"], "min_years": 3}'
```

`data/resumes/` ships 12 short, clearly-fake sample resumes with varied skills, years of experience, and locations so the search filters have something real to differentiate.

## Scaling workers

```bash
docker compose up --scale worker=3
```

## Testing

```bash
make test-unit          # no Docker required
make test-integration   # requires Docker (testcontainers-go)
```
