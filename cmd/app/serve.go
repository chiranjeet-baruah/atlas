package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"

	"resumesearch/internal/adapter/driven/kafka"
	"resumesearch/internal/adapter/driven/modelclient"
	"resumesearch/internal/adapter/driven/postgres"
	httpdriver "resumesearch/internal/adapter/driver/http"
	webdriver "resumesearch/internal/adapter/driver/web"
	"resumesearch/internal/service"
)

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			pool, err := postgres.MigrateAndConnect(ctx, requireEnv("DATABASE_URL"), "migrations")
			if err != nil {
				return fmt.Errorf("migrate and connect to postgres: %w", err)
			}
			defer pool.Close()

			repo := postgres.NewRepository(pool)

			producer, err := kafka.NewProducer(ctx, splitBrokers(requireEnv("KAFKA_BROKERS")))
			if err != nil {
				return fmt.Errorf("create kafka producer: %w", err)
			}
			defer producer.Close()

			model := modelclient.New(requireEnv("LLM_URL"), requireEnv("LLM_MODEL"), requireEnv("LLM_API_KEY"), requireEnv("EMBED_URL"), requireEnv("EMBED_MODEL"))

			uploadUC := service.NewUploadResumesUseCase(repo, producer, requireEnv("STORAGE_DIR"))
			statusUC := service.NewGetStatusUseCase(repo)
			searchUC := service.NewSearchResumesUseCase(repo, model)

			router := gin.Default()
			router.POST("/resumes/batch", httpdriver.NewUploadHandler(uploadUC))
			router.GET("/resumes/:id", httpdriver.NewStatusHandler(statusUC))
			router.GET("/resumes/:id/file", httpdriver.NewResumeFileHandler(statusUC))
			router.GET("/resumes/batch/:batch_id", httpdriver.NewBatchStatusHandler(statusUC))
			router.POST("/search", httpdriver.NewSearchHandler(searchUC))
			webdriver.New(router, uploadUC, statusUC, statusUC, searchUC)

			port := os.Getenv("HTTP_PORT")
			if port == "" {
				port = "8080"
			}
			srv := &http.Server{Addr: ":" + port, Handler: router}

			serveErr := make(chan error, 1)
			go func() {
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					serveErr <- err
					return
				}
				serveErr <- nil
			}()

			select {
			case <-ctx.Done():
			case err := <-serveErr:
				return err
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("shut down HTTP server: %w", err)
			}
			return <-serveErr
		},
	}
}
