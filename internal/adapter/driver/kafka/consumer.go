package kafka

import (
	"context"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/exp/slog"

	"resumesearch/internal/constants"
)

// Consumer drives ProcessResumeUseCase.Run by reading resume-ingest
// events from Kafka as part of a shared consumer group — running
// multiple worker replicas (`docker compose up --scale worker=3`)
// distributes partitions across them automatically.
type Consumer struct {
	client *kgo.Client
}

func NewConsumer(brokers []string) (*Consumer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(constants.ConsumerGroup),
		kgo.ConsumeTopics(constants.KafkaTopic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}
	return &Consumer{client: client}, nil
}

// Consume polls for records until ctx is done, invoking handler for each
// one, then commits offsets for the whole batch. A handler error is logged
// and the offset is committed anyway: the resume's failure is already
// durably recorded as domain.StatusFailed by ProcessResumeUseCase, so
// retrying it here would just reprocess a known failure and — worse —
// stall every message queued behind it on the partition.
//
// Each record gets a bounded constants.ResumeProcessingTimeout: without it,
// one hung LLM/embedding call or wedged pdftotext subprocess (both
// context-aware, so this actually bounds them) would block this single
// processing loop forever — no more resumes are ever processed until the
// process is manually restarted, with no error ever logged since the call
// never returns.
func (c *Consumer) Consume(ctx context.Context, handler func(ctx context.Context, resumeID string) error) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if fetches.IsClientClosed() {
			return nil
		}

		for _, fetchErr := range fetches.Errors() {
			if isTransientFetchError(fetchErr.Err) {
				slog.WarnContext(ctx, "transient kafka fetch error, continuing", "topic", fetchErr.Topic, "partition", fetchErr.Partition, "error", fetchErr.Err)
				continue
			}
			return fmt.Errorf("fetch topic %s partition %d: %w", fetchErr.Topic, fetchErr.Partition, fetchErr.Err)
		}

		fetches.EachRecord(func(rec *kgo.Record) {
			resumeID := string(rec.Value)
			recordCtx, cancel := context.WithTimeout(ctx, constants.ResumeProcessingTimeout)
			defer cancel()
			if err := handler(recordCtx, resumeID); err != nil {
				slog.ErrorContext(ctx, "resume processing failed", "resume_id", resumeID, "error", err)
			}
		})

		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			return fmt.Errorf("commit offsets: %w", err)
		}
	}
}

// isTransientFetchError reports whether err is one of franz-go's own
// documented informational fetch-error classes (*kgo.ErrDataLoss,
// *kgo.ErrGroupSession) that the client already recovers from internally —
// per franz-go's Fetches.Errors() doc comment, these are "worth logging and
// investigating, but not worth restarting the client for." Every other
// class (auth/ACL failures, batch corruption, unknown errors) stays fatal
// so a real problem doesn't get silently skipped.
func isTransientFetchError(err error) bool {
	if _, ok := errors.AsType[*kgo.ErrDataLoss](err); ok {
		return true
	}
	if _, ok := errors.AsType[*kgo.ErrGroupSession](err); ok {
		return true
	}
	return false
}

func (c *Consumer) Close() error {
	c.client.Close()
	return nil
}
