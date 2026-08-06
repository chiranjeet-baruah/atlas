package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/constants"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)

// uploadRunner is the minimal seam the handler needs — satisfied by
// *service.UploadResumesUseCase, but expressed as an interface here so
// the handler is testable without the full use case.
type uploadRunner interface {
	Run(ctx context.Context, files []service.UploadFile) (dto.UploadBatchResponse, error)
}

func NewUploadHandler(uc uploadRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Bound total request body size before parsing it — without this,
		// a single oversized request is fully buffered/spilled-to-disk by
		// Go's multipart parser with no upper limit, and every part is
		// still read fully into memory below.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, constants.MaxUploadBytes)

		form, err := c.MultipartForm()
		if err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart form: " + err.Error()})
			return
		}

		fileHeaders := form.File["files"]
		if len(fileHeaders) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no files provided under form field 'files'"})
			return
		}
		if len(fileHeaders) > constants.MaxUploadFiles {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("too many files: %d exceeds the limit of %d", len(fileHeaders), constants.MaxUploadFiles)})
			return
		}

		files := make([]service.UploadFile, 0, len(fileHeaders))
		for _, fh := range fileHeaders {
			f, err := fh.Open()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "failed to open " + fh.Filename})
				return
			}
			content, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read " + fh.Filename})
				return
			}
			files = append(files, service.UploadFile{Filename: fh.Filename, Content: content})
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
