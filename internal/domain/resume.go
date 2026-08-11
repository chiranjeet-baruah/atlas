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

// BatchSummary aggregates one batch's resumes by status, for the
// processing tab's batch list. CreatedAt is the earliest resume's
// created_at in the batch, since every resume in a batch is inserted at
// upload time and the batch itself has no separate row.
type BatchSummary struct {
	BatchID    string
	Total      int
	Pending    int
	Processing int
	Done       int
	Failed     int
	CreatedAt  time.Time
}
