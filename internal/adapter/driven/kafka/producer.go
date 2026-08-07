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

// Producer publishes events for all three pipeline stages — implements
// service.EventPublisher, service.ExtractedPublisher, and
// service.ClassifiedPublisher. See publish's doc comment for the message
// shape.
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

// NewProducer connects to brokers and ensures every pipeline stage's topic
// exists before returning — a fresh `docker compose up` starts Kafka with
// no topics pre-created, and the first publish to any of them must not
// race that. Both cmd/app/serve.go and cmd/app/worker.go construct a
// Producer at startup (the worker needs one to publish extract/classify
// results to the next stage's topic), so calling this from both covers a
// cold-started worker that must not depend on serve having run first.
func NewProducer(ctx context.Context, brokers []string) (*Producer, error) {
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}

	for _, topic := range allTopics {
		if err := ensureTopicWithRetry(ctx, client, topic); err != nil {
			client.Close()
			return nil, err
		}
	}

	return &Producer{client: client}, nil
}

// allTopics is every topic any pipeline stage publishes to. Keep this in
// sync with the stage table in decisions.md if a stage is ever added.
var allTopics = []string{
	constants.KafkaTopic,
	constants.TopicResumeExtracted,
	constants.TopicResumeClassified,
}

func (p *Producer) PublishResumeIngest(ctx context.Context, resumeID string) error {
	return p.publish(ctx, constants.KafkaTopic, resumeID)
}

func (p *Producer) PublishResumeExtracted(ctx context.Context, resumeID string) error {
	return p.publish(ctx, constants.TopicResumeExtracted, resumeID)
}

func (p *Producer) PublishResumeClassified(ctx context.Context, resumeID string) error {
	return p.publish(ctx, constants.TopicResumeClassified, resumeID)
}

// publish is shared by all three PublishResume* methods: the message key
// is the resumeID so Kafka's partitioning keeps all events for one resume
// ordered on a given topic, while still allowing parallel workers across
// resumes.
func (p *Producer) publish(ctx context.Context, topic, resumeID string) error {
	record := &kgo.Record{Topic: topic, Key: []byte(resumeID), Value: []byte(resumeID)}
	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("publish event for %s to topic %s: %w", resumeID, topic, err)
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
