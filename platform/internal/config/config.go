package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for FleetOS services.
type Config struct {
	// Service identity
	ServiceName string
	Environment string // dev, staging, prod

	// gRPC server
	GRPCPort int

	// HTTP/REST server
	HTTPPort int

	// Kafka
	KafkaBrokers       []string
	KafkaTelemetryTopic string
	KafkaCommandTopic   string
	KafkaGroupID        string

	// PostgreSQL
	PostgresDSN string

	// Redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Simulator
	SimRobotCount    int
	SimTickInterval  time.Duration

	// Inference
	InferenceEndpoint string
	InferenceTimeout  time.Duration

	// S3 / MinIO
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3UseSSL    bool

	// Auth
	JWTSecret     string
	OAuth2Issuer  string

	// Rate limiting
	RateLimitRPS   int
	RateLimitBurst int

	// Temporal (control plane)
	TemporalHostPort  string
	TemporalNamespace string
	TemporalEnabled   bool

	// Training (Kubeflow / K8s)
	TrainingImage     string
	TrainingNamespace string
	TrainingEnabled   bool
	TrainingCallbackURL string
}

func Load() *Config {
	return &Config{
		ServiceName:         getEnv("SERVICE_NAME", "fleetos"),
		Environment:         getEnv("ENVIRONMENT", "dev"),
		GRPCPort:            getEnvInt("GRPC_PORT", 50051),
		HTTPPort:            getEnvInt("HTTP_PORT", 8080),
		KafkaBrokers:        []string{getEnv("KAFKA_BROKERS", "localhost:9092")},
		KafkaTelemetryTopic: getEnv("KAFKA_TELEMETRY_TOPIC", "robot.telemetry"),
		KafkaCommandTopic:   getEnv("KAFKA_COMMAND_TOPIC", "robot.commands"),
		KafkaGroupID:        getEnv("KAFKA_GROUP_ID", "fleetos-ingestion"),
		PostgresDSN:         getEnv("POSTGRES_DSN", "postgres://fleetos:fleetos@localhost:5432/fleetos?sslmode=disable"),
		RedisAddr:           getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:       getEnv("REDIS_PASSWORD", ""),
		RedisDB:             getEnvInt("REDIS_DB", 0),
		SimRobotCount:       getEnvInt("SIM_ROBOT_COUNT", 10),
		SimTickInterval:     time.Duration(getEnvInt("SIM_TICK_MS", 100)) * time.Millisecond,
		S3Endpoint:          getEnv("S3_ENDPOINT", "localhost:9000"),
		S3Bucket:            getEnv("S3_BUCKET", "fleetos-telemetry"),
		S3AccessKey:         getEnv("S3_ACCESS_KEY", "fleetos"),
		S3SecretKey:         getEnv("S3_SECRET_KEY", "fleetos123"),
		S3UseSSL:            getEnv("S3_USE_SSL", "false") == "true",
		InferenceEndpoint:   getEnv("INFERENCE_ENDPOINT", "localhost:8081"),
		InferenceTimeout:    time.Duration(getEnvInt("INFERENCE_TIMEOUT_MS", 5000)) * time.Millisecond,
		JWTSecret:           getEnv("JWT_SECRET", "dev-secret-change-me"),
		OAuth2Issuer:        getEnv("OAUTH2_ISSUER", "https://auth.fleetos.dev"),
		RateLimitRPS:        getEnvInt("RATE_LIMIT_RPS", 100),
		RateLimitBurst:      getEnvInt("RATE_LIMIT_BURST", 200),
		TemporalHostPort:    getEnv("TEMPORAL_HOST_PORT", "localhost:7233"),
		TemporalNamespace:   getEnv("TEMPORAL_NAMESPACE", "default"),
		TemporalEnabled:     getEnv("TEMPORAL_ENABLED", "false") == "true",
		TrainingImage:       getEnv("TRAINING_IMAGE", "fleetos/training:latest"),
		TrainingNamespace:   getEnv("TRAINING_NAMESPACE", "default"),
		TrainingEnabled:     getEnv("TRAINING_ENABLED", "false") == "true",
		TrainingCallbackURL: getEnv("TRAINING_CALLBACK_URL", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			slog.Warn("invalid int config, using default", "key", key, "value", v, "default", fallback, "error", err)
			return fallback
		}
		return i
	}
	return fallback
}
