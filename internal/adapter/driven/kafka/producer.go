package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"resumesearch/internal/constants"
)

// Producer publishes resume-ingest events. The message key is the
// resumeID so Kafka's partitioning keeps all events for one resume
// ordered, while still allowing parallel workers across resumes.
type Producer struct {
	client *kgo.Client
}

// topicCreateRetries and topicCreateBackoff bound how long NewProducer will
// wait for the broker's controller to accept CreateTopics on startup.
// `docker compose up` only gates on Kafka's own healthcheck, which can
// report ready slightly before the controller can service admin requests —
// without a retry, NewProducer (and therefore `serve`) fails hard on that
// narrow window instead of recovering.
const (
	topicCreateRetries = 5
	topicCreateBackoff = 2 * time.Second
)

// NewProducer connects to brokers and ensures constants.KafkaTopic exists
// before returning — a fresh `docker compose up` starts Kafka with no
// topics pre-created, and the first publish must not race that.
func NewProducer(ctx context.Context, brokers []string) (*Producer, error) {
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}

	if err := ensureTopicWithRetry(ctx, client, constants.KafkaTopic); err != nil {
		client.Close()
		return nil, err
	}

	return &Producer{client: client}, nil
}

func (p *Producer) PublishResumeIngest(ctx context.Context, resumeID string) error {
	record := &kgo.Record{Topic: constants.KafkaTopic, Key: []byte(resumeID), Value: []byte(resumeID)}
	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("publish resume ingest event for %s: %w", resumeID, err)
	}
	return nil
}

func (p *Producer) Close() error {
	p.client.Close()
	return nil
}

// ensureTopicWithRetry creates topic with 3 partitions (matching the
// documented `docker compose up --scale worker=3`) if it doesn't already
// exist, retrying on failure since the broker's healthcheck can report
// ready slightly before its controller can service admin requests. Safe to
// call on every producer startup: a concurrent create by another replica
// surfaces as kerr.TopicAlreadyExists, which is treated as success.
//
// Deliberately does not call the kadm.Client's Close: it wraps and shares
// the same *kgo.Client the Producer keeps using afterward, and Close on
// the admin wrapper closes that underlying client too.
func ensureTopicWithRetry(ctx context.Context, client *kgo.Client, topic string) error {
	admin := kadm.NewClient(client)

	var lastErr error
	for attempt := 1; attempt <= topicCreateRetries; attempt++ {
		lastErr = createTopicOnce(ctx, admin, topic)
		if lastErr == nil {
			return nil
		}
		if attempt == topicCreateRetries {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("create topic %s: %w", topic, ctx.Err())
		case <-time.After(topicCreateBackoff):
		}
	}
	return fmt.Errorf("create topic %s after %d attempts: %w", topic, topicCreateRetries, lastErr)
}

func createTopicOnce(ctx context.Context, admin *kadm.Client, topic string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := admin.CreateTopics(reqCtx, 3, 1, nil, topic)
	if err != nil {
		return err
	}
	for _, r := range resp {
		if r.Err != nil && !errors.Is(r.Err, kerr.TopicAlreadyExists) {
			return r.Err
		}
	}
	return nil
}
