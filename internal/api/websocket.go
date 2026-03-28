package api

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dimuthu/robot-fleet/internal/middleware"
	"github.com/gorilla/websocket"
)

var allowedOrigins = func() map[string]bool {
	origins := map[string]bool{
		"http://localhost:5173": true,
		"http://localhost:8080": true,
		"http://localhost:3000": true,
	}
	if extra := os.Getenv("WS_ALLOWED_ORIGINS"); extra != "" {
		for _, o := range strings.Split(extra, ",") {
			origins[strings.TrimSpace(o)] = true
		}
	}
	return origins
}()

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // Non-browser clients (curl, SDKs)
		}
		return allowedOrigins[origin]
	},
}

// WebSocketTelemetry upgrades to WebSocket and streams live telemetry.
// Authenticates via api_key query parameter since browsers can't set headers on WebSocket.
func (h *Handler) WebSocketTelemetry(w http.ResponseWriter, r *http.Request) {
	// Authenticate via query param (browsers can't set WebSocket headers)
	apiKey := r.URL.Query().Get("api_key")
	if apiKey == "" {
		http.Error(w, `{"error":"api_key query parameter required"}`, http.StatusUnauthorized)
		return
	}

	keyInfo, valid := h.apiKeys.Validate(apiKey)
	if !valid {
		http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
		return
	}

	tenantID := keyInfo.TenantID
	robotID := r.URL.Query().Get("robot_id")

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	slog.Info("websocket connected", "tenant", tenantID, "robot_filter", robotID)
	middleware.WebSocketConnections.Inc()
	defer middleware.WebSocketConnections.Dec()

	// Subscribe to Redis pub/sub for real-time telemetry
	channel := "telemetry:all"
	if robotID != "" {
		channel = "telemetry:" + robotID
	}

	sub := h.cache.Subscribe(r.Context(), channel)
	defer sub.Close()

	ch := sub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
				slog.Debug("websocket write error", "error", err)
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
