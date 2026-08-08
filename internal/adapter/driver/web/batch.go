package web

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/dto"
)

// batchStatusRunner is the seam the batch handlers need — satisfied by
// *service.GetStatusUseCase.
type batchStatusRunner interface {
	ByBatchID(ctx context.Context, batchID string) (dto.BatchStatusResponse, error)
}

// NewBatchPageHandler renders the full batch status page.
func NewBatchPageHandler(uc batchStatusRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := uc.ByBatchID(c.Request.Context(), c.Param("batch_id"))
		if err != nil {
			renderError(c, err)
			return
		}
		c.HTML(http.StatusOK, "batch_page", resp)
	}
}

// NewBatchRowsHandler renders just the status table, for the batch page's
// Refresh button to swap in via htmx. There is no automatic polling — the
// button fires this on click only.
func NewBatchRowsHandler(uc batchStatusRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := uc.ByBatchID(c.Request.Context(), c.Param("batch_id"))
		if err != nil {
			renderError(c, err)
			return
		}
		c.HTML(http.StatusOK, "batch_rows", resp)
	}
}
