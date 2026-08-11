package multipartform

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/constants"
	"resumesearch/internal/service"
)

// InputError is returned by ParseUploadFiles for any caller-fixable problem
// with the request: an oversized body, a malformed multipart body, zero
// files, too many files, or a file that couldn't be read. TooLarge is true
// only for the oversized-body case — the one case a caller must map to 413
// rather than 400 (JSON) or an inline form error (web).
type InputError struct {
	Message  string
	TooLarge bool
}

func (e *InputError) Error() string { return e.Message }

// ParseUploadFiles bounds the request body to constants.MaxUploadBytes,
// parses the "files" multipart field, and reads every part fully into
// memory. Both internal/adapter/driver/http and internal/adapter/driver/web
// call this for their respective upload entry points, so
// MaxUploadBytes/MaxUploadFiles are enforced identically by both and exist
// in exactly one place.
func ParseUploadFiles(c *gin.Context) ([]service.UploadFile, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, constants.MaxUploadBytes)

	form, err := c.MultipartForm()
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, &InputError{Message: "request body too large", TooLarge: true}
		}
		return nil, &InputError{Message: "invalid multipart form: " + err.Error()}
	}

	fileHeaders := form.File["files"]
	if len(fileHeaders) == 0 {
		return nil, &InputError{Message: "no files provided under form field 'files'"}
	}
	if len(fileHeaders) > constants.MaxUploadFiles {
		return nil, &InputError{Message: fmt.Sprintf("too many files: %d exceeds the limit of %d", len(fileHeaders), constants.MaxUploadFiles)}
	}

	files := make([]service.UploadFile, 0, len(fileHeaders))
	for _, fh := range fileHeaders {
		f, err := fh.Open()
		if err != nil {
			return nil, &InputError{Message: "failed to open " + fh.Filename}
		}
		content, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, &InputError{Message: "failed to read " + fh.Filename}
		}
		files = append(files, service.UploadFile{Filename: fh.Filename, Content: content})
	}

	return files, nil
}
