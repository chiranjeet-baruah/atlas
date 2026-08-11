package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"resumesearch/internal/constants"
	"resumesearch/internal/dto"
)

// batchListRunner is the seam the processing handlers need — satisfied by
// *service.GetStatusUseCase.
type batchListRunner interface {
	ListBatches(ctx context.Context) ([]dto.BatchSummary, error)
}

// processingRowsView is the "processing_page"/"processing_rows" templates'
// data. LookupError and Error are deliberately separate fields, not one
// shared "Error": LookupError is set only by NewBatchLookupHandler on a bad
// batch_id and renders above the form, outside the table; Error is set
// only by the rows-fragment use-case-error path and renders inside the
// table alongside ErrorSlug. Sharing one field would let a bad lookup
// input blank out the batch table, since processing_rows' {{if .Error}}
// branch skips {{range .Batches}} entirely.
type processingRowsView struct {
	LookupError string
	Error       string
	ErrorSlug   string
	Batches     []dto.BatchSummary
	Limit       int
	Truncated   bool
}

func newProcessingRowsView(batches []dto.BatchSummary) processingRowsView {
	return processingRowsView{
		Batches:   batches,
		Limit:     constants.ProcessingBatchListLimit,
		Truncated: len(batches) == constants.ProcessingBatchListLimit,
	}
}

// NewProcessingPageHandler renders the full processing page: the batch-ID
// lookup form plus the batch list. A use-case error here is a full-page
// navigation failure, so it goes through renderError like every other
// full-page handler.
func NewProcessingPageHandler(uc batchListRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		batches, err := uc.ListBatches(c.Request.Context())
		if err != nil {
			renderError(c, err)
			return
		}
		c.HTML(http.StatusOK, "processing_page", newProcessingRowsView(batches))
	}
}

// NewProcessingRowsHandler renders just the batch table, for the
// processing page's Refresh button to swap in via htmx. There is no
// automatic polling, matching the batch page's Refresh button. See the
// package doc comment for why a use-case error here renders inline at 200
// instead of through renderError.
func NewProcessingRowsHandler(uc batchListRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		batches, err := uc.ListBatches(c.Request.Context())
		if err != nil {
			_, slug, message := classifyError(c.Request.Context(), err)
			c.HTML(http.StatusOK, "processing_rows", processingRowsView{Error: message, ErrorSlug: slug})
			return
		}
		c.HTML(http.StatusOK, "processing_rows", newProcessingRowsView(batches))
	}
}

// NewBatchLookupHandler jumps from a batch ID typed into the processing
// page's lookup form straight to that batch's status page. batch_id is
// validated as a UUID before being used: resumes.batch_id is a Postgres
// UUID column, so a typo'd, non-UUID value would otherwise reach
// GetByBatchID's WHERE clause, fail to encode, and surface as a generic
// 500 instead of a clear "invalid batch ID" message. This validation only
// matters because the lookup form is a new way to reach /ui/batch/:id with
// arbitrary user input — the post-upload redirect always used a real,
// generated ID.
//
// On success, redirects (303, matching the post-upload redirect in
// upload.go) to the batch page. On a missing or invalid batch_id,
// re-renders "processing_page" at 200 with LookupError set — the same
// "re-render the full page with an inline error" pattern upload.go uses
// for its own bad-request case, and 200 for the same reason: a browser
// form submission has no status code to react to.
func NewBatchLookupHandler(uc batchListRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		batchID := strings.TrimSpace(c.Query("batch_id"))
		if batchID != "" {
			if _, err := uuid.Parse(batchID); err == nil {
				c.Redirect(http.StatusSeeOther, "/ui/batch/"+url.PathEscape(batchID))
				return
			}
		}

		lookupErr := "Please enter a batch ID."
		if batchID != "" {
			lookupErr = "That is not a valid batch ID."
		}

		batches, err := uc.ListBatches(c.Request.Context())
		if err != nil {
			renderError(c, err)
			return
		}
		view := newProcessingRowsView(batches)
		view.LookupError = lookupErr
		c.HTML(http.StatusOK, "processing_page", view)
	}
}
