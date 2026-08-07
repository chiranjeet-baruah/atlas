package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/exp/slog"

	"resumesearch/internal/adapter/driven/kafka"
	"resumesearch/internal/adapter/driven/modelclient"
	"resumesearch/internal/adapter/driven/pdf"
	"resumesearch/internal/adapter/driven/postgres"
	kafkadriver "resumesearch/internal/adapter/driver/kafka"
	"resumesearch/internal/constants"
	"resumesearch/internal/service"
)

func workerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run the Kafka consumers that process uploaded resumes through the extract/classify/embed pipeline",
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
			brokers := splitBrokers(requireEnv("KAFKA_BROKERS"))

			// kafka.Producer implements service.EventPublisher,
			// service.ExtractedPublisher, and service.ClassifiedPublisher —
			// one publisher covers every stage's publish target. Its
			// constructor also ensures all 3 pipeline topics exist; a
			// cold-started worker must not depend on `serve` having done
			// that first.
			producer, err := kafka.NewProducer(ctx, brokers)
			if err != nil {
				return fmt.Errorf("create kafka producer: %w", err)
			}
			defer producer.Close()

			extractUC := service.NewExtractResumeUseCase(repo, extractor, producer)
			classifyUC := service.NewClassifyResumeUseCase(repo, model, producer)
			embedUC := service.NewEmbedResumeUseCase(repo, model)
			sweepUC := service.NewRedriveSweepUseCase(repo, producer, producer, producer)

			extractConsumer, err := kafkadriver.NewConsumer(brokers, constants.KafkaTopic, constants.ConsumerGroup, constants.ExtractStageTimeout)
			if err != nil {
				return fmt.Errorf("create extract stage consumer: %w", err)
			}
			defer func() { _ = extractConsumer.Close() }()

			classifyConsumer, err := kafkadriver.NewConsumer(brokers, constants.TopicResumeExtracted, constants.GroupResumeClassify, constants.ClassifyStageTimeout)
			if err != nil {
				return fmt.Errorf("create classify stage consumer: %w", err)
			}
			defer func() { _ = classifyConsumer.Close() }()

			embedConsumer, err := kafkadriver.NewConsumer(brokers, constants.TopicResumeClassified, constants.GroupResumeEmbed, constants.EmbedStageTimeout)
			if err != nil {
				return fmt.Errorf("create embed stage consumer: %w", err)
			}
			defer func() { _ = embedConsumer.Close() }()

			return runPipeline(ctx, []func(context.Context) error{
				func(ctx context.Context) error { return extractConsumer.Consume(ctx, extractUC.Run) },
				func(ctx context.Context) error { return classifyConsumer.Consume(ctx, classifyUC.Run) },
				func(ctx context.Context) error { return embedConsumer.Consume(ctx, embedUC.Run) },
				func(ctx context.Context) error { return runSweeper(ctx, sweepUC) },
			})
		},
	}
}

// runPipeline runs every fn concurrently, sharing one derived context: if
// any fn returns (successfully or with an error) before ctx itself is
// done, that's treated as a fatal condition for the whole worker — the
// derived context is canceled so the others wind down promptly instead of
// three-quarters of the pipeline quietly running on while one stage is
// dead. Deliberately not the errgroup package: this is the one place that
// pattern is needed, and a manual fan-out/fan-in matches the rest of this
// project's minimalism (see decisions.md).
func runPipeline(ctx context.Context, fns []func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(fns))
	for _, fn := range fns {
		go func(fn func(context.Context) error) {
			err := fn(runCtx)
			cancel()
			errs <- err
		}(fn)
	}

	var firstErr error
	for range fns {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// runSweeper runs sweepUC on a constants.SweepInterval ticker until ctx is
// done. A single sweep failing (e.g. one Kafka publish among a batch, per
// RedriveSweepUseCase.Run's doc comment) is logged and retried next tick
// rather than treated as fatal — the whole point of periodic sweeping is
// that a transient failure gets another chance shortly.
func runSweeper(ctx context.Context, sweepUC *service.RedriveSweepUseCase) error {
	ticker := time.NewTicker(constants.SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := sweepUC.Run(ctx); err != nil {
				slog.ErrorContext(ctx, "redrive sweep failed", "error", err)
			}
		}
	}
}
