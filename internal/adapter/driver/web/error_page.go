package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/exp/slog"

	"resumesearch/internal/domain"
)

// classifyError maps err to an HTTP status, a stable slug identifying its
// category, and a generic message safe to render in a browser. The real
// err is logged via slog under the same slug so it can still be found —
// classifyError is the one place in this package allowed to see the raw
// error text; every caller renders only the returned message.
func classifyError(ctx context.Context, err error) (status int, slug, message string) {
	if errors.Is(err, domain.ErrNotFound) {
		return http.StatusNotFound, "not-found", "We couldn't find what you were looking for."
	}
	slog.ErrorContext(ctx, "web request failed", "slug", "internal-error", "error", err)
	return http.StatusInternalServerError, "internal-error", "Something went wrong. Please try again."
}

// renderError classifies err via classifyError and renders the matching
// full-page template — the HTML-rendering equivalent of
// internal/adapter/driver/http's writeResumeError.
func renderError(c *gin.Context, err error) {
	status, slug, message := classifyError(c.Request.Context(), err)
	if status == http.StatusNotFound {
		c.HTML(status, "not_found_page", gin.H{})
		return
	}
	c.HTML(status, "error_page", gin.H{"Message": message, "Slug": slug})
}
