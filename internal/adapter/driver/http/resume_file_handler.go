package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/domain"
	"resumesearch/internal/dto"
)

type resumeFileRunner interface {
	FileByID(ctx context.Context, id string) (dto.ResumeFileInfo, error)
}

// NewResumeFileHandler streams a resume's original uploaded file back to
// the caller.
//
// Only .pdf is served inline: uploads have no content-type allowlist
// enforced anywhere except at write time (UploadResumesUseCase.Run), so a
// resume row that predates that check, or any file whose extension doesn't
// match its actual content, must never be rendered inline — that would let
// arbitrary uploaded content execute in the browser at this app's origin.
// Anything else is forced to download via Content-Disposition: attachment,
// which browsers honor regardless of Content-Type.
func NewResumeFileHandler(uc resumeFileRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		info, err := uc.FileByID(c.Request.Context(), c.Param("id"))
		if err != nil {
			writeResumeFileError(c, err)
			return
		}

		if strings.EqualFold(filepath.Ext(info.Filename), ".pdf") {
			// Defense in depth alongside Content-Disposition below: even if
			// the served bytes aren't actually a PDF, nosniff stops the
			// browser from reinterpreting them as an executable type.
			c.Header("X-Content-Type-Options", "nosniff")
			c.File(info.FilePath)
			return
		}

		c.FileAttachment(info.FilePath, info.Filename)
	}
}

// writeResumeFileError deliberately does not reuse writeResumeError
// (status_handler.go), which puts raw err.Error() into a 500 body: every
// other handler in this package is called by a trusted JSON client, but
// this one is now linked directly from the search results page as
// <a href target="_blank">, so a 500 here renders in an end user's browser
// tab. A DB outage must not leak connection strings or hostnames there —
// same reasoning as the web UI's classifyError, applied to this one
// browser-reachable JSON handler.
func writeResumeFileError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "resume not found"})
		return
	}
	slog.ErrorContext(c.Request.Context(), "resume file request failed", "error", err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
}
