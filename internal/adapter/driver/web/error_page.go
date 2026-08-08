package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/domain"
)

// renderError maps domain.ErrNotFound to the not-found page (404) and
// everything else to the generic error page (500) — the HTML-rendering
// equivalent of internal/adapter/driver/http's writeResumeError.
func renderError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		c.HTML(http.StatusNotFound, "not_found_page", gin.H{})
		return
	}
	c.HTML(http.StatusInternalServerError, "error_page", gin.H{"Error": err.Error()})
}
