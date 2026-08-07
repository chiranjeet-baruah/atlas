package dto

import "resumesearch/internal/domain"

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

type BatchStatusResponse struct {
	BatchID string           `json:"batch_id"`
	Resumes []StatusResponse `json:"resumes"`
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
	// Distance is a vector distance, not a similarity score: lower means a
	// closer match. Results are already sorted best-first server-side
	// (repository.go's Search, ORDER BY best_distance ASC) — a client
	// re-sorting this field descending, as the name "score" would invite,
	// would invert the ranking.
	Distance float32 `json:"distance"`
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

func FromSearchResult(sr domain.SearchResult) SearchResultDTO {
	return SearchResultDTO{
		ID:              sr.Resume.ID,
		Filename:        sr.Resume.Filename,
		Skills:          sr.Resume.Skills,
		YearsExperience: sr.Resume.YearsExperience,
		Location:        sr.Resume.Location,
		Distance:        sr.BestDistance,
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
