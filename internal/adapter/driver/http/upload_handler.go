package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/adapter/driver/multipartform"
	"resumesearch/internal/service"
)

func NewUploadHandler(uc service.UploadRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		files, err := multipartform.ParseUploadFiles(c)
		if err != nil {
			var inputErr *multipartform.InputError
			if errors.As(err, &inputErr) && inputErr.TooLarge {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": inputErr.Message})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		resp, err := uc.Run(c.Request.Context(), files)
		if err != nil {
			// resp may be partially populated (batch ID plus every resume
			// already committed before the failure) — surface it instead
			// of discarding it, so the caller isn't left with no way to
			// find resumes that already exist and are already processing.
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "batch_id": resp.BatchID, "resumes": resp.Resumes})
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}
