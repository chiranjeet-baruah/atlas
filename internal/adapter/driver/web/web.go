package web

import (
	"github.com/gin-gonic/gin"

	"resumesearch/internal/service"
)

// New wires the web UI onto router: parses embedded templates, serves
// embedded static assets, and registers every /ui/* route. uploadUC,
// statusUC, listUC, and searchUC are the same use-case instances the JSON
// API (internal/adapter/driver/http) is already wired to in
// cmd/app/serve.go — this adapter is a second, HTML-rendering driver in
// front of the same use cases, not a second copy of them. statusUC and
// listUC are typically the same *service.GetStatusUseCase instance,
// passed twice because each handler group declares its own narrow
// interface (service.BatchStatusReader vs batchListRunner).
func New(router *gin.Engine, uploadUC service.UploadRunner, statusUC service.BatchStatusReader, listUC batchListRunner, searchUC service.SearchRunner) {
	router.SetHTMLTemplate(ParseTemplates())
	router.StaticFS("/ui/static", StaticFS())

	router.GET("/ui/upload", NewUploadPageHandler())
	router.POST("/ui/upload", NewUploadSubmitHandler(uploadUC))
	router.GET("/ui/batch/:batch_id", NewBatchPageHandler(statusUC))
	router.GET("/ui/batch/:batch_id/rows", NewBatchRowsHandler(statusUC))
	router.GET("/ui/processing", NewProcessingPageHandler(listUC))
	router.GET("/ui/processing/rows", NewProcessingRowsHandler(listUC))
	router.GET("/ui/processing/lookup", NewBatchLookupHandler(listUC))
	router.GET("/ui/search", NewSearchPageHandler())
	router.POST("/ui/search", NewSearchSubmitHandler(searchUC))
}
