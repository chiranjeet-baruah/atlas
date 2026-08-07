-- migrations/0002_stage_tracking.up.sql
-- Tracks which pipeline stage a resume has reached (EXTRACT/CLASSIFY/EMBED)
-- and how many times the redrive sweeper has reclaimed it. An explicit
-- column, not inferred from which other columns are populated: inference
-- can't distinguish "embed stage never ran" from "embed stage ran and
-- correctly produced zero chunks" (a real case for near-threshold text),
-- which would make the sweeper redrive a correctly-completed resume
-- forever. See decisions.md.
ALTER TABLE resumes
    ADD COLUMN stage TEXT NOT NULL DEFAULT 'EXTRACT',
    ADD COLUMN redrive_count INT NOT NULL DEFAULT 0;
