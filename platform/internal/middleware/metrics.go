package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fleetos_api_requests_total",
		Help: "Total HTTP requests by method, path, and status",
	}, []string{"method", "path", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "fleetos_api_request_duration_seconds",
		Help:    "HTTP request duration in seconds",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"method", "path"})

	activeConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fleetos_api_active_connections",
		Help: "Number of active HTTP connections",
	})

	TelemetryPacketsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "fleetos_telemetry_packets_total",
		Help: "Total telemetry packets processed",
	})

	RobotsTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fleetos_robots_total",
		Help: "Total number of registered robots",
	})

	RobotsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fleetos_robots_active",
		Help: "Number of active robots",
	})

	RobotsError = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fleetos_robots_error",
		Help: "Number of robots in error state",
	})

	AvgBatteryLevel = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fleetos_avg_battery_level",
		Help: "Average battery level across all robots (0-1)",
	})

	KafkaConsumerLag = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fleetos_kafka_consumer_lag",
		Help: "Kafka consumer lag (messages behind)",
	})

	InferenceDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleetos_inference_duration_seconds",
		Help:    "AI inference request duration",
		Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
	})

	TenantAPIUsage = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fleetos_tenant_api_calls_total",
		Help: "API calls per tenant",
	}, []string{"tenant_id"})

	WebSocketConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "fleetos_websocket_connections",
		Help: "Active WebSocket connections",
	})

	CommandDispatchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "fleetos_command_dispatch_duration_seconds",
		Help:    "End-to-end command dispatch latency (API to robot ack)",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1, 2},
	})

	CommandsDispatched = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "fleetos_commands_dispatched_total",
		Help: "Total commands dispatched to robots",
	}, []string{"command_type", "status"})
)

// MetricsHandler returns the Prometheus metrics HTTP handler.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// Metrics middleware records request count and duration.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		activeConnections.Inc()
		defer activeConnections.Dec()

		start := time.Now()
		rw := &metricsResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(rw.statusCode)
		path := normalizePath(r.URL.Path)

		httpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}

type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *metricsResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker so WebSocket upgrades work through the metrics middleware.
func (w *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("upstream ResponseWriter does not implement http.Hijacker")
}

// normalizePath reduces cardinality by replacing IDs with {id}.
func normalizePath(path string) string {
	// /api/v1/robots/robot-0001/command → /api/v1/robots/{id}/command
	parts := splitPath(path)
	for i, p := range parts {
		if (len(p) >= 6 && p[:6] == "robot-") || (len(p) > 4 && isNumeric(p)) {
			parts[i] = "{id}"
		}
	}
	return joinPath(parts)
}

func splitPath(p string) []string {
	var parts []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

func joinPath(parts []string) string {
	return "/" + strings.Join(parts, "/")
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
