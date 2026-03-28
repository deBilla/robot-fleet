package store

import (
	"context"
	"testing"
	"time"
)

func TestS3Key_Format(t *testing.T) {
	ts := time.Date(2026, 3, 28, 14, 30, 0, 0, time.UTC)
	key := S3TelemetryKey("robot-0001", "state_update", ts)

	// Verify structure: telemetry/{year}/{month}/{day}/{hour}/{robot}/{type}/{nanos}.pb
	expectedPrefix := "telemetry/2026/03/28/14/robot-0001/state_update/"
	if key[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("expected prefix %s, got %s", expectedPrefix, key)
	}
	if key[len(key)-3:] != ".pb" {
		t.Errorf("expected .pb suffix, got %s", key)
	}
}

func TestS3Key_Partitioning(t *testing.T) {
	// Keys for the same robot, same hour should share a prefix (efficient S3 listing)
	ts1 := time.Date(2026, 3, 28, 14, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 3, 28, 14, 59, 0, 0, time.UTC)
	ts3 := time.Date(2026, 3, 28, 15, 0, 0, 0, time.UTC)

	k1 := S3TelemetryKey("r1", "state_update", ts1)
	k2 := S3TelemetryKey("r1", "state_update", ts2)
	k3 := S3TelemetryKey("r1", "state_update", ts3)

	// Same hour prefix
	prefix1 := k1[:len("telemetry/2026/03/28/14/")]
	prefix2 := k2[:len("telemetry/2026/03/28/14/")]
	if prefix1 != prefix2 {
		t.Errorf("same hour keys should share prefix: %s vs %s", prefix1, prefix2)
	}

	// Different hour
	prefix3 := k3[:len("telemetry/2026/03/28/15/")]
	if prefix1 == prefix3 {
		t.Error("different hour keys should have different prefixes")
	}
}

func TestS3Store_Interface(t *testing.T) {
	// Verify S3Store implements BlobStore interface at compile time
	var _ BlobStore = (*S3Store)(nil)
}

func TestNewS3Store_InvalidEndpoint(t *testing.T) {
	// Should still create the client (connection is lazy)
	s, err := NewS3Store(context.Background(), S3Config{
		Endpoint:  "localhost:19999",
		Bucket:    "test",
		AccessKey: "test",
		SecretKey: "test",
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}
