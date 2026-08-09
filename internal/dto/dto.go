package dto

import (
	"math"
	"time"

	"resumesearch/internal/domain"
)

type ResumeRef struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
}

type UploadBatchResponse struct {
	BatchID string      `json:"batch_id"`
	Resumes []ResumeRef `json:"resumes"`
}

type StatusResponse struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Status   string `json:"status"`
	// Stage names which point in the extract/classify/embed pipeline this
	// resume has reached — answers "which stage is this stuck in" from the
	// status endpoint directly, instead of grepping 3 workers' logs.
	// RedriveCount is deliberately not exposed here: it's internal sweeper
	// bookkeeping, not something a client needs.
	Stage        string `json:"stage"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// ResumeFileInfo is the minimal data a file-serving handler needs — the
// on-disk path to stream, plus the original filename for the extension
// check and download name. Deliberately not a JSON-facing DTO: it's never
// serialized, only passed from the use case to the handler.
type ResumeFileInfo struct {
	Filename string
	FilePath string
}

type BatchStatusResponse struct {
	BatchID string           `json:"batch_id"`
	Resumes []StatusResponse `json:"resumes"`
}

// BatchSummary is one batch's aggregate status counts, for the processing
// tab's batch list. CreatedAt stays a time.Time rather than a pre-formatted
// string: this package is shared with the JSON API, and display formatting
// belongs at the render layer, not baked into the transport type.
type BatchSummary struct {
	BatchID    string    `json:"batch_id"`
	Total      int       `json:"total"`
	Pending    int       `json:"pending"`
	Processing int       `json:"processing"`
	Done       int       `json:"done"`
	Failed     int       `json:"failed"`
	CreatedAt  time.Time `json:"created_at"`
}

type SearchRequest struct {
	Query          string   `json:"query" binding:"required"`
	RequiredSkills []string `json:"required_skills"`
	MinYears       *float64 `json:"min_years"`
	Location       string   `json:"location"`
}

type SearchResultDTO struct {
	ID              string   `json:"id"`
	Filename        string   `json:"filename"`
	Skills          []string `json:"skills"`
	YearsExperience float64  `json:"years_experience"`
	Location        string   `json:"location"`
	// MatchPercentage is 0-100, higher means a better match. Derived from
	// pgvector cosine distance (repository.go's Search, best_distance = 1 -
	// cosine similarity) via matchPercentage below. Unlike raw distance,
	// higher-is-better here actually matches the name, so — unlike the old
	// "distance" field — this one is safe to think of the way its name
	// suggests. Results are still sorted best-first server-side (ORDER BY
	// best_distance ASC, which is also highest-percentage-first); a client
	// never needs to re-sort this.
	MatchPercentage int `json:"match_percentage"`
}

type SearchResponse struct {
	Results []SearchResultDTO `json:"results"`
}

func FromResume(r domain.Resume) StatusResponse {
	return StatusResponse{
		ID:           r.ID,
		Filename:     r.Filename,
		Status:       string(r.Status),
		Stage:        r.Stage,
		ErrorMessage: r.ErrorMessage,
	}
}

// FromResumes always returns a non-nil (possibly empty) slice so the JSON
// response serializes "resumes":[] rather than "resumes":null.
func FromResumes(resumes []domain.Resume) []StatusResponse {
	out := make([]StatusResponse, 0, len(resumes))
	for _, r := range resumes {
		out = append(out, FromResume(r))
	}
	return out
}

func FromResumeFile(r domain.Resume) ResumeFileInfo {
	return ResumeFileInfo{
		Filename: r.Filename,
		FilePath: r.FilePath,
	}
}

func FromBatchSummary(b domain.BatchSummary) BatchSummary {
	return BatchSummary{
		BatchID:    b.BatchID,
		Total:      b.Total,
		Pending:    b.Pending,
		Processing: b.Processing,
		Done:       b.Done,
		Failed:     b.Failed,
		CreatedAt:  b.CreatedAt,
	}
}

// FromBatchSummaries always returns a non-nil (possibly empty) slice so the
// JSON response serializes "batches":[] rather than "batches":null, and so
// the web view's {{range .Batches}} "No batches yet" fallback works on a
// nil-safe value.
func FromBatchSummaries(summaries []domain.BatchSummary) []BatchSummary {
	out := make([]BatchSummary, 0, len(summaries))
	for _, b := range summaries {
		out = append(out, FromBatchSummary(b))
	}
	return out
}

func FromSearchResult(sr domain.SearchResult) SearchResultDTO {
	return SearchResultDTO{
		ID:              sr.Resume.ID,
		Filename:        sr.Resume.Filename,
		Skills:          sr.Resume.Skills,
		YearsExperience: sr.Resume.YearsExperience,
		Location:        sr.Resume.Location,
		MatchPercentage: matchPercentage(sr.BestDistance),
	}
}

// matchPercentage converts a pgvector cosine distance (1 - cosine
// similarity) into a 0-100 value where higher is a better match. Clamped at
// both ends: cosine distance ranges [0, 2] (2 meaning perfectly opposite
// vectors), which maps to [-100, 100] before clamping — a percentage has no
// meaningful negative value to show a user, and 100 is already the
// best-possible match.
func matchPercentage(distance float32) int {
	pct := int(math.Round((1 - float64(distance)) * 100))
	switch {
	case pct < 0:
		return 0
	case pct > 100:
		return 100
	default:
		return pct
	}
}

// FromSearchResults always returns a non-nil (possibly empty) slice so the
// JSON response serializes "results":[] rather than "results":null.
func FromSearchResults(results []domain.SearchResult) []SearchResultDTO {
	out := make([]SearchResultDTO, 0, len(results))
	for _, r := range results {
		out = append(out, FromSearchResult(r))
	}
	return out
}
