package domain

import (
	"errors"
	"time"
)

// ErrNotFound is returned by ResumeRepository lookups when no row matches.
// Driver adapters (HTTP) translate this into a 404; anything else is a 500.
var ErrNotFound = errors.New("resume: not found")

// ErrStatusNotRecorded wraps a processing error when the attempt to record
// it (writeStatus, marking the resume FAILED) itself failed — e.g. the
// database was genuinely unreachable. The Kafka consumer checks for this
// with errors.Is and skips committing the offset so the message redelivers,
// instead of committing it as though the failure were durably recorded.
var ErrStatusNotRecorded = errors.New("resume: failure status could not be recorded")

type Status string

const (
	StatusPending    Status = "PENDING"
	StatusProcessing Status = "PROCESSING"
	StatusDone       Status = "DONE"
	StatusFailed     Status = "FAILED"
)

// Stage tracks which point in the extract → classify → embed pipeline a
// resume has reached. It is sweeper bookkeeping, not a status: a resume can
// be StatusProcessing at any of the three stages, or StatusDone/StatusFailed
// after any of them.
const (
	StageExtract  = "EXTRACT"
	StageClassify = "CLASSIFY"
	StageEmbed    = "EMBED"
)

// Resume is the core entity: one uploaded PDF and everything derived from it.
type Resume struct {
	ID              string
	BatchID         string
	Filename        string
	FilePath        string
	Status          Status
	Stage           string
	RedriveCount    int
	ErrorMessage    string
	RawText         string
	Skills          []string
	YearsExperience float64
	Location        string
	ExtractedJSON   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Chunk is one embedded slice of a resume's text.
type Chunk struct {
	ID         int64
	ResumeID   string
	ChunkIndex int
	ChunkText  string
	Embedding  []float32
}

// ExtractedFields is what the LLM extraction step returns.
type ExtractedFields struct {
	Skills          []string `json:"skills"`
	YearsExperience float64  `json:"years_experience"`
	Location        string   `json:"location"`
}

// SearchFilters are the hard predicates applied alongside vector similarity.
type SearchFilters struct {
	RequiredSkills []string
	MinYears       *float64
	Location       string
}

// SearchResult pairs a Resume with its best-matching-chunk distance (lower = closer).
type SearchResult struct {
	Resume       Resume
	BestDistance float32
}
