package ingestion

import (
	"context"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
)

// KafkaProducer wraps a kafka writer for publishing telemetry data.
type KafkaProducer struct {
	writer *kafka.Writer
	topic  string
}

func NewKafkaProducer(brokers []string, topic string) (*KafkaProducer, error) {
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.Hash{}, // partition by key (robot_id)
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
		Async:        false,
		RequiredAcks: kafka.RequireAll,
	}
	slog.Info("kafka producer created", "brokers", brokers, "topic", topic)
	return &KafkaProducer{writer: w, topic: topic}, nil
}

// Publish sends a message to Kafka, keyed by robotID for partition affinity.
func (p *KafkaProducer) Publish(robotID string, data []byte) error {
	return p.writer.WriteMessages(context.Background(), kafka.Message{
		Key:   []byte(robotID),
		Value: data,
	})
}

func (p *KafkaProducer) Close() {
	if err := p.writer.Close(); err != nil {
		slog.Error("failed to close kafka writer", "error", err)
	}
}

// KafkaConsumer wraps a kafka reader for consuming telemetry data.
type KafkaConsumer struct {
	reader *kafka.Reader
}

func NewKafkaConsumer(brokers []string, topic, groupID string) *KafkaConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 1e3,  // 1KB
		MaxBytes: 10e6, // 10MB
	})
	return &KafkaConsumer{reader: r}
}

// Consume reads messages from Kafka and passes them to the handler function.
func (c *KafkaConsumer) Consume(ctx context.Context, handler func(key, value []byte) error) error {
	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Error("kafka read error", "error", err)
			continue
		}
		if err := handler(msg.Key, msg.Value); err != nil {
			slog.Error("message handler error", "key", string(msg.Key), "error", err)
		}
	}
}

func (c *KafkaConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		slog.Error("failed to close kafka reader", "error", err)
	}
}
