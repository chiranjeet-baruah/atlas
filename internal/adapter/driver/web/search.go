package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/dto"
)

// searchRunner is the seam the search handlers need — satisfied by
// *service.SearchResumesUseCase.
type searchRunner interface {
	Run(ctx context.Context, req dto.SearchRequest) (dto.SearchResponse, error)
}

// searchResultsView is the "search_results" fragment's template data — it
// covers both a validation failure (Error set, no use case call) and a
// successful search (Results set), so both cases swap into the same #results
// target.
type searchResultsView struct {
	Error   string
	Results []dto.SearchResultDTO
}

// NewSearchPageHandler renders the empty search form.
func NewSearchPageHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "search_page", gin.H{})
	}
}

// NewSearchSubmitHandler validates the form, calls uc.Run, and renders the
// results fragment in the exact order the use case returns it. Distance is
// a vector distance (lower = closer), already sorted best-first
// server-side (see internal/dto/dto.go's SearchResultDTO.Distance comment)
// — this handler must never re-sort it or label it "score".
func NewSearchSubmitHandler(uc searchRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := strings.TrimSpace(c.PostForm("query"))
		if query == "" {
			c.HTML(http.StatusOK, "search_results", searchResultsView{Error: "enter a search query"})
			return
		}

		var requiredSkills []string
		if raw := strings.TrimSpace(c.PostForm("required_skills")); raw != "" {
			for _, s := range strings.Split(raw, ",") {
				if s = strings.TrimSpace(s); s != "" {
					requiredSkills = append(requiredSkills, s)
				}
			}
		}

		var minYears *float64
		if raw := strings.TrimSpace(c.PostForm("min_years")); raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				c.HTML(http.StatusOK, "search_results", searchResultsView{Error: "min years must be a number"})
				return
			}
			minYears = &v
		}

		req := dto.SearchRequest{
			Query:          query,
			RequiredSkills: requiredSkills,
			MinYears:       minYears,
			Location:       strings.TrimSpace(c.PostForm("location")),
		}

		resp, err := uc.Run(c.Request.Context(), req)
		if err != nil {
			renderError(c, err)
			return
		}

		c.HTML(http.StatusOK, "search_results", searchResultsView{Results: resp.Results})
	}
}
