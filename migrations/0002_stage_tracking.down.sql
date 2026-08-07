-- migrations/0002_stage_tracking.down.sql
ALTER TABLE resumes
    DROP COLUMN stage,
    DROP COLUMN redrive_count;
