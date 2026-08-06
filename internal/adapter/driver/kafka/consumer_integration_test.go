//go:build integration

package kafka_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	kafkadriver "resumesearch/internal/adapter/driver/kafka"
	"resumesearch/internal/constants"
)

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

			consumer, err := kafkadriver.NewConsumer(brokers)
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
