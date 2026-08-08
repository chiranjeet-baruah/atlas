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
	BatchID string
	Error   string
	Resumes []dto.StatusResponse
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
// button fires this on click only.
//
// A use-case error here renders inline inside the fragment at 200, not
// through renderError: htmx does not swap a non-2xx response into its
// target by default, so a 404/500 full-page response from this endpoint
// would leave the Refresh button's target empty with the error invisible
// to the user. Rendering the error inside "batch_rows" at 200 keeps it
// visible.
func NewBatchRowsHandler(uc batchStatusRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := uc.ByBatchID(c.Request.Context(), c.Param("batch_id"))
		if err != nil {
			c.HTML(http.StatusOK, "batch_rows", batchRowsView{Error: err.Error()})
			return
		}
		c.HTML(http.StatusOK, "batch_rows", batchRowsView{BatchID: resp.BatchID, Resumes: resp.Resumes})
	}
}
