package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/adapter/driver/multipartform"
	"resumesearch/internal/service"
)

// NewUploadPageHandler renders the empty upload form.
func NewUploadPageHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "upload_page", gin.H{})
	}
}

// NewUploadSubmitHandler parses the multipart upload via
// multipartform.ParseUploadFiles (shared with the JSON API's upload
// handler), calls uc.Run, and redirects to the new batch's status page.
// Any parse failure re-renders the form with an inline error at 200 —
// there is no 413/400 split here the way the JSON API has one, since a
// browser form submission has no equivalent of a JSON error status to
// react to; the message is enough.
func NewUploadSubmitHandler(uc service.UploadRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		files, err := multipartform.ParseUploadFiles(c)
		if err != nil {
			c.HTML(http.StatusOK, "upload_page", gin.H{"Error": err.Error()})
			return
		}

		resp, err := uc.Run(c.Request.Context(), files)
		if err != nil {
			renderError(c, err)
			return
		}

		c.Redirect(http.StatusSeeOther, "/ui/batch/"+resp.BatchID)
	}
}
