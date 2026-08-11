package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"resumesearch/internal/constants"
	"resumesearch/internal/dto"
	"resumesearch/internal/service"
)

func NewSearchHandler(uc service.SearchRunner) gin.HandlerFunc {
	return func(c *gin.Context) {
		// A search request is a short query plus a few filter fields and
		// should never legitimately approach this size — bound it before
		// binding so an oversized body can't be fully buffered into memory.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, constants.MaxSearchBodyBytes)

		var req dto.SearchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		resp, err := uc.Run(c.Request.Context(), req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}
