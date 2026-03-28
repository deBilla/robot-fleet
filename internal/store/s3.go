package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// BlobStore defines the interface for object/blob storage operations.
type BlobStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	List(ctx context.Context, prefix string, maxKeys int) ([]string, error)
}

// S3Config holds configuration for S3-compatible storage.
type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// S3Store implements BlobStore using MinIO/S3-compatible storage.
type S3Store struct {
	client *minio.Client
	bucket string
}

// NewS3Store creates a new S3-compatible object store client.
func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}

	// Ensure bucket exists
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		slog.Warn("s3 bucket check failed (will retry on first write)", "bucket", cfg.Bucket, "error", err)
	} else if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			slog.Warn("s3 bucket creation failed", "bucket", cfg.Bucket, "error", err)
		} else {
			slog.Info("s3 bucket created", "bucket", cfg.Bucket)
		}
	}

	slog.Info("connected to s3", "endpoint", cfg.Endpoint, "bucket", cfg.Bucket)
	return &S3Store{client: client, bucket: cfg.Bucket}, nil
}

// Put stores data at the given key.
func (s *S3Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

// Get retrieves data at the given key.
func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("s3 read %s: %w", key, err)
	}
	return data, nil
}

// List returns keys matching the given prefix.
func (s *S3Store) List(ctx context.Context, prefix string, maxKeys int) ([]string, error) {
	var keys []string
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for obj := range s.client.ListObjects(ctx, s.bucket, opts) {
		if obj.Err != nil {
			return nil, fmt.Errorf("s3 list %s: %w", prefix, obj.Err)
		}
		keys = append(keys, obj.Key)
		if maxKeys > 0 && len(keys) >= maxKeys {
			break
		}
	}
	return keys, nil
}

// S3TelemetryKey generates a partitioned S3 key for telemetry data.
// Partition scheme: telemetry/{year}/{month}/{day}/{hour}/{robot_id}/{event_type}/{timestamp_ns}.pb
// This enables efficient time-range queries and per-robot filtering.
func S3TelemetryKey(robotID, eventType string, ts time.Time) string {
	return fmt.Sprintf("telemetry/%s/%s/%s/%s.pb",
		ts.Format("2006/01/02/15"),
		robotID,
		eventType,
		fmt.Sprintf("%d", ts.UnixNano()),
	)
}
