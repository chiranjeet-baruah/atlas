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

// batchRowsView is the "batch_page"/"batch_rows" templates' data. Error is
// set only by NewBatchRowsHandler's fragment-error path; a zero-value Error
// renders the normal resume table.
type batchRowsView struct {
	BatchID   string
	Error     string
	ErrorSlug string
	Resumes   []dto.StatusResponse
}

// NewBatchPageHandler renders the full batch status page. A use-case error
// here is a full-page navigation failure, so it goes through renderError
// (404 for ErrNotFound, 500 otherwise) like every other full-page handler.
func NewBatchPageHandler(uc batchStatusRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := uc.ByBatchID(c.Request.Context(), c.Param("batch_id"))
		if err != nil {
			renderError(c, err)
			return
		}
		c.HTML(http.StatusOK, "batch_page", batchRowsView{BatchID: resp.BatchID, Resumes: resp.Resumes})
	}
}

// NewBatchRowsHandler renders just the status table, for the batch page's
// Refresh button to swap in via htmx. There is no automatic polling — the
// button fires this on click only. See the package doc comment for why a
// use-case error here renders inline at 200 instead of through renderError.
func NewBatchRowsHandler(uc batchStatusRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := uc.ByBatchID(c.Request.Context(), c.Param("batch_id"))
		if err != nil {
			_, slug, message := classifyError(c.Request.Context(), err)
			c.HTML(http.StatusOK, "batch_rows", batchRowsView{Error: message, ErrorSlug: slug})
			return
		}
		c.HTML(http.StatusOK, "batch_rows", batchRowsView{BatchID: resp.BatchID, Resumes: resp.Resumes})
	}
}
