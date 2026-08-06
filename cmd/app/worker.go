package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"resumesearch/internal/adapter/driven/modelclient"
	"resumesearch/internal/adapter/driven/pdf"
	"resumesearch/internal/adapter/driven/postgres"
	kafkadriver "resumesearch/internal/adapter/driver/kafka"
	"resumesearch/internal/service"
)

func workerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run the Kafka consumer that processes uploaded resumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			pool, err := postgres.MigrateAndConnect(ctx, requireEnv("DATABASE_URL"), "migrations")
			if err != nil {
				return fmt.Errorf("migrate and connect to postgres: %w", err)
			}
			defer pool.Close()

			repo := postgres.NewRepository(pool)
			model := modelclient.New(requireEnv("LLM_URL"), requireEnv("LLM_MODEL"), requireEnv("EMBED_URL"), requireEnv("EMBED_MODEL"))
			extractor := pdf.NewExtractor()

			processUC := service.NewProcessResumeUseCase(repo, model, extractor)

			consumer, err := kafkadriver.NewConsumer(splitBrokers(requireEnv("KAFKA_BROKERS")))
			if err != nil {
				return fmt.Errorf("create kafka consumer: %w", err)
			}
			defer consumer.Close()

			return consumer.Consume(ctx, processUC.Run)
		},
	}
}
