-- migrations/0001_init.up.sql
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

CREATE TABLE IF NOT EXISTS resumes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id UUID NOT NULL,
    filename TEXT NOT NULL,
    file_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    error_message TEXT,
    raw_text TEXT,
    skills TEXT[] NOT NULL DEFAULT '{}',
    years_experience DOUBLE PRECISION,
    location TEXT,
    extracted_json JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_resumes_skills ON resumes USING GIN (skills);
CREATE INDEX IF NOT EXISTS idx_resumes_batch ON resumes (batch_id);

-- Embedding dimension (768) must match constants.EmbeddingDimension in Go
-- exactly — it's the verified output size of ai/nomic-embed-text-v1.5.
CREATE TABLE IF NOT EXISTS resume_chunks (
    id BIGSERIAL PRIMARY KEY,
    resume_id UUID NOT NULL REFERENCES resumes(id) ON DELETE CASCADE,
    chunk_index INT NOT NULL,
    chunk_text TEXT NOT NULL,
    embedding VECTOR(768) NOT NULL,
    UNIQUE (resume_id, chunk_index)
);
-- Deliberately no HNSW/IVFFlat index here: at ~1000 resumes, sequential
-- scan over resume_chunks is exact and fast (<10ms). Revisit past ~10k rows.
