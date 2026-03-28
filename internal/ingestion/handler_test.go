package ingestion

import (
	"testing"
)

func TestNewTelemetryHandler(t *testing.T) {
	h := NewTelemetryHandler(nil)
	if h == nil {
		t.Fatal("handler should not be nil")
	}
	if h.packetCount.Load() != 0 {
		t.Errorf("initial packet count should be 0, got %d", h.packetCount.Load())
	}
}

func TestNewKafkaConsumer(t *testing.T) {
	c := NewKafkaConsumer([]string{"localhost:9092"}, "test-topic", "test-group")
	if c == nil {
		t.Fatal("consumer should not be nil")
	}
	c.Close()
}
