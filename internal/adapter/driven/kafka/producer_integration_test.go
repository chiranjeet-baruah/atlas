//go:build integration

package kafka_test

import (
	"context"
	"testing"
	"time"

	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kgo"

	adapterkafka "resumesearch/internal/adapter/driven/kafka"
	"resumesearch/internal/constants"
)

func startKafka(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()

	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.0", tckafka.WithClusterID("test-cluster"))
	if err != nil {
		t.Fatalf("failed to start kafka container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("failed to get brokers: %v", err)
	}
	return brokers
}

func TestNewProducer_CreatesTopicIfMissing(t *testing.T) {
	brokers := startKafka(t)

	producer, err := adapterkafka.NewProducer(context.Background(), brokers)
	if err != nil {
		t.Fatalf("NewProducer failed: %v", err)
	}
	t.Cleanup(func() { _ = producer.Close() })
}

func TestPublishResumeIngest_MessageIsReadable(t *testing.T) {
	brokers := startKafka(t)
	ctx := context.Background()

	producer, err := adapterkafka.NewProducer(context.Background(), brokers)
	if err != nil {
		t.Fatalf("NewProducer failed: %v", err)
	}
	t.Cleanup(func() { _ = producer.Close() })

	reader, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup("test-reader"),
		kgo.ConsumeTopics(constants.KafkaTopic),
	)
	if err != nil {
		t.Fatalf("failed to create reader client: %v", err)
	}
	t.Cleanup(reader.Close)

	cases := []struct {
		name     string
		resumeID string
	}{
		{name: "typical resume id", resumeID: "resume-123"},
		{name: "another resume id keeps key/value symmetry", resumeID: "resume-456"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := producer.PublishResumeIngest(ctx, tc.resumeID); err != nil {
				t.Fatalf("PublishResumeIngest failed: %v", err)
			}

			readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			fetches := reader.PollFetches(readCtx)
			if errs := fetches.Errors(); len(errs) > 0 {
				t.Fatalf("fetch error: %v", errs[0].Err)
			}

			records := fetches.Records()
			if len(records) == 0 {
				t.Fatal("expected at least one record, got none")
			}
			rec := records[0]
			if string(rec.Value) != tc.resumeID {
				t.Errorf("expected message value %q, got %q", tc.resumeID, string(rec.Value))
			}
			if string(rec.Key) != tc.resumeID {
				t.Errorf("expected message key %q (for per-resume ordering), got %q", tc.resumeID, string(rec.Key))
			}
		})
	}
}
