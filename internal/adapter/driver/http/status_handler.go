package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)

type statusByIDRunner interface {
	ByID(ctx context.Context, id string) (dto.StatusResponse, error)
}

func NewStatusHandler(uc statusByIDRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := uc.ByID(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeResumeError(c, err)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

func NewBatchStatusHandler(uc service.BatchStatusReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := uc.ByBatchID(c.Request.Context(), c.Param("batch_id"))
		if err != nil {
			writeResumeError(c, err)
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}

// writeResumeError maps domain.ErrNotFound to 404 and everything else (a
// database outage, a network error) to 500 — a repository failure must
// not look like "this resume doesn't exist" to the caller.
func writeResumeError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "resume not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
