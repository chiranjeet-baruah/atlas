package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/exp/slog"

	"resumesearch/internal/domain"
)

// Consumer drives one pipeline stage's use case by reading that stage's
// topic as part of a shared consumer group — running multiple worker
// replicas (`docker compose up --scale worker=3`) distributes partitions
// across them automatically. Each of the 3 pipeline stages constructs its
// own Consumer (own topic, own group, own handlerTimeout).
type Consumer struct {
	client         *kgo.Client
	handlerTimeout time.Duration
}

// NewConsumer connects to brokers as a member of group, consuming topic.
// handlerTimeout bounds every handler call (see Consume) — the caller
// picks it per stage (e.g. constants.ExtractStageTimeout,
// constants.ClassifyStageTimeout, constants.EmbedStageTimeout), since each
// stage's work has a different realistic worst case.
func NewConsumer(brokers []string, topic, group string, handlerTimeout time.Duration) (*Consumer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}
	return &Consumer{client: client, handlerTimeout: handlerTimeout}, nil
}

// Consume polls for records until ctx is done, invoking handler for each
// one bounded by handlerTimeout, then commits offsets for the whole batch —
// unless at least one handler call in the batch failed in a way that could
// not be durably recorded (see the skipCommit comment below).
//
// A handler error is logged and, ordinarily, the offset is still committed:
// the resume's failure is durably recorded as domain.StatusFailed by the
// use case (via the shared writeStatus/failResume helpers in
// internal/service), so retrying it here would just reprocess a known
// failure and — worse — stall every message queued behind it on the
// partition.
//
// handlerTimeout bounds every handler call: without it, one hung LLM/
// embedding call or wedged pdftotext subprocess (both context-aware, so
// this actually bounds them) would block this single processing loop
// forever — no more resumes are ever processed until the process is
// manually restarted, with no error ever logged since the call never
// returns.
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

		skipCommit := false
		fetches.EachRecord(func(rec *kgo.Record) {
			resumeID := string(rec.Value)
			recordCtx, cancel := context.WithTimeout(ctx, c.handlerTimeout)
			defer cancel()
			if err := handler(recordCtx, resumeID); err != nil {
				slog.ErrorContext(ctx, "resume processing failed", "resume_id", resumeID, "error", err)
				if errors.Is(err, domain.ErrStatusNotRecorded) {
					skipCommit = true
				}
			}
		})

		if skipCommit {
			// At least one record's failure could not be durably recorded
			// (e.g. the database was unreachable when writeStatus tried to
			// mark it FAILED) — committing anyway would tell Kafka this
			// batch is done when its outcome was never written anywhere,
			// breaking the invariant the rest of this comment's first
			// paragraph relies on. Kafka only supports committing a
			// per-partition watermark, not individual records, so this
			// withholds the WHOLE batch's offsets rather than just the one
			// record that hit this — every stage's Run is safe to redeliver
			// (the isTerminal(Resume.Status) guard, plus SaveChunks's
			// upsert), so reprocessing a few already-succeeded records
			// alongside the one that needed it is a self-correcting cost,
			// not a correctness problem.
			slog.WarnContext(ctx, "skipping offset commit: at least one record's failure could not be durably recorded, batch will redeliver")
			continue
		}

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
