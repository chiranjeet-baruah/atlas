//go:build integration

package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	kafkadriver "resumesearch/internal/adapter/driver/kafka"
	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
)

// testHandlerTimeout is a generous per-handler budget for these tests —
// large enough that it never legitimately fires, so a real hang shows up
// as a test timeout rather than a silently-misleading pass.
const testHandlerTimeout = 15 * time.Second

// createTopic explicitly creates topic before the test writes to it — the
// confluent-local image used here does not auto-create topics on first
// produce, so relying on that would make every seed write racy.
func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("create kafka client: %v", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := admin.CreateTopics(ctx, 1, 1, nil, topic)
	if err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for _, r := range resp {
		if r.Err != nil {
			t.Fatalf("create topic %s: %v", topic, r.Err)
		}
	}
}

func TestConsume(t *testing.T) {
	cases := []struct {
		name       string
		records    []*kgo.Record
		handlerErr error // returned by the handler for every message in this case
		wantCalls  map[string]int
	}{
		{
			name: "invokes handler once per distinct message",
			records: []*kgo.Record{
				{Key: []byte("resume-1"), Value: []byte("resume-1"), Topic: constants.KafkaTopic},
				{Key: []byte("resume-2"), Value: []byte("resume-2"), Topic: constants.KafkaTopic},
			},
			wantCalls: map[string]int{"resume-1": 1, "resume-2": 1},
		},
		{
			name: "invokes handler once per redelivered message — dedup is the use case's job, not the consumer's",
			records: []*kgo.Record{
				{Key: []byte("resume-3"), Value: []byte("resume-3"), Topic: constants.KafkaTopic},
				{Key: []byte("resume-3"), Value: []byte("resume-3"), Topic: constants.KafkaTopic},
			},
			wantCalls: map[string]int{"resume-3": 2},
		},
		{
			name: "a handler error does not stop the consumer from processing the next message",
			records: []*kgo.Record{
				{Key: []byte("resume-4"), Value: []byte("resume-4"), Topic: constants.KafkaTopic},
				{Key: []byte("resume-5"), Value: []byte("resume-5"), Topic: constants.KafkaTopic},
			},
			handlerErr: errors.New("processing failed"),
			wantCalls:  map[string]int{"resume-4": 1, "resume-5": 1},
		},
	}

	ctx := context.Background()
	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.0")
	if err != nil {
		t.Fatalf("failed to start kafka container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("failed to get brokers: %v", err)
	}

	createTopic(t, brokers, constants.KafkaTopic)

	writer, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("create writer client: %v", err)
	}
	t.Cleanup(writer.Close)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := writer.ProduceSync(ctx, tc.records...).FirstErr(); err != nil {
				t.Fatalf("failed to seed messages: %v", err)
			}

			consumer, err := kafkadriver.NewConsumer(brokers, constants.KafkaTopic, constants.ConsumerGroup, testHandlerTimeout)
			if err != nil {
				t.Fatalf("NewConsumer failed: %v", err)
			}
			t.Cleanup(func() { _ = consumer.Close() })

			var mu sync.Mutex
			calls := make(map[string]int)

			consumeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()

			done := make(chan struct{})
			go func() {
				_ = consumer.Consume(consumeCtx, func(ctx context.Context, resumeID string) error {
					mu.Lock()
					calls[resumeID]++
					total := 0
					for _, c := range calls {
						total += c
					}
					mu.Unlock()
					if total >= len(tc.records) {
						cancel()
					}
					return tc.handlerErr
				})
				close(done)
			}()

			<-done

			mu.Lock()
			defer mu.Unlock()
			for id, want := range tc.wantCalls {
				if calls[id] != want {
					t.Errorf("handler called %d times for %s, want %d", calls[id], id, want)
				}
			}
		})
	}
}

// TestConsume_ErrStatusNotRecordedSkipsCommitSoMessageRedelivers locks in
// the offset-commit policy fix: unlike an ordinary processing error (which
// commits anyway — dealt with above), a handler error matching
// domain.ErrStatusNotRecorded means the failure itself couldn't be durably
// recorded, so the offset must be withheld and the message redelivered to
// the next consumer in the same group. This can't be observed within a
// single running Consumer (once a record leaves PollFetches, that session
// won't re-fetch it locally regardless of commit state) — it's only
// observable by restarting: a second Consumer joining the same group must
// still receive the message.
func TestConsume_ErrStatusNotRecordedSkipsCommitSoMessageRedelivers(t *testing.T) {
	ctx := context.Background()
	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.0")
	if err != nil {
		t.Fatalf("failed to start kafka container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("failed to get brokers: %v", err)
	}

	const topic = "resume.test.not-recorded"
	const group = "resume-test-not-recorded-group"
	createTopic(t, brokers, topic)

	writer, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("create writer client: %v", err)
	}
	t.Cleanup(writer.Close)
	if err := writer.ProduceSync(ctx, &kgo.Record{Key: []byte("resume-x"), Value: []byte("resume-x"), Topic: topic}).FirstErr(); err != nil {
		t.Fatalf("failed to seed message: %v", err)
	}

	firstCalls := runConsumerOnce(t, brokers, topic, group, func(ctx context.Context, resumeID string) error {
		return fmt.Errorf("db down: %w", domain.ErrStatusNotRecorded)
	})
	if firstCalls != 1 {
		t.Fatalf("expected the first consumer to receive the message once, got %d calls", firstCalls)
	}

	secondCalls := runConsumerOnce(t, brokers, topic, group, func(ctx context.Context, resumeID string) error {
		return nil
	})
	if secondCalls != 1 {
		t.Errorf("expected a second consumer in the same group to still receive the message (offset withheld), got %d calls", secondCalls)
	}
}

// runConsumerOnce runs a fresh Consumer against topic/group until handler
// has been called at least once, then stops it and returns the call count.
func runConsumerOnce(t *testing.T, brokers []string, topic, group string, handler func(ctx context.Context, resumeID string) error) int {
	t.Helper()

	consumer, err := kafkadriver.NewConsumer(brokers, topic, group, testHandlerTimeout)
	if err != nil {
		t.Fatalf("NewConsumer failed: %v", err)
	}
	defer func() { _ = consumer.Close() }()

	consumeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var calls int
	done := make(chan struct{})
	go func() {
		_ = consumer.Consume(consumeCtx, func(ctx context.Context, resumeID string) error {
			calls++
			cancel()
			return handler(ctx, resumeID)
		})
		close(done)
	}()
	<-done

	return calls
}
