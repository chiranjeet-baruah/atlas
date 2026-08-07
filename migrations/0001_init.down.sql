-- migrations/0001_init.down.sql
DROP TABLE IF EXISTS resume_chunks;
DROP TABLE IF EXISTS resumes;
DROP EXTENSION IF EXISTS pgcrypto;
DROP EXTENSION IF EXISTS vector;
