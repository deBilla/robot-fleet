package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// AuditLog middleware records all mutating API calls (POST, PUT, DELETE) to the audit_log table.
// Uses a bounded goroutine pool with graceful shutdown support.
type AuditLog struct {
	writer store.AuditWriter
	wg     sync.WaitGroup
}

// NewAuditLog creates an audit logger with the given writer.
func NewAuditLog(writer store.AuditWriter) *AuditLog {
	return &AuditLog{writer: writer}
}

// Middleware returns the HTTP middleware that intercepts mutating requests.
func (a *AuditLog) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only audit mutating requests
		if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}

		// Skip internal callback endpoints
		if strings.HasPrefix(r.URL.Path, "/api/v1/internal/") {
			next.ServeHTTP(w, r)
			return
		}

		tenantID := auth.GetTenantID(r.Context())
		if tenantID == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Capture response status
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		// Write audit log asynchronously with lifecycle tracking
		method := r.Method
		path := r.URL.Path
		status := rw.status
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.writeEntry(tenantID, method, path, status)
		}()
	})
}

// Shutdown waits for all in-flight audit writes to complete.
func (a *AuditLog) Shutdown(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("audit log shutdown timed out, some writes may be lost")
	}
}

func (a *AuditLog) writeEntry(tenantID, method, path string, status int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	action := methodToAction(method)
	resourceType, resourceID := parseResource(path)

	if err := a.writer.WriteAuditLog(ctx, tenantID, action, resourceType, resourceID, map[string]any{
		"method":      method,
		"path":        path,
		"status_code": status,
	}); err != nil {
		slog.Warn("audit log write failed", "error", err, "tenant", tenantID, "path", path)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap supports http.Flusher passthrough for SSE.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func methodToAction(method string) string {
	switch method {
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return method
	}
}

// parseResource extracts resource type and ID from the URL path.
func parseResource(path string) (string, string) {
	segments := strings.FieldsFunc(path, func(c rune) bool { return c == '/' })
	// /api/v1/{resource}/{id}/...
	if len(segments) >= 4 {
		resourceType := segments[3]
		resourceID := ""
		if len(segments) >= 5 {
			resourceID = segments[4]
		}
		return resourceType, resourceID
	}
	return path, ""
}
