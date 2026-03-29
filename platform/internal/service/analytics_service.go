package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

const analyticsCacheTTL = 60 * time.Second

// AnalyticsService provides cached access to OLAP analytics data.
type AnalyticsService interface {
	GetFleetAnalytics(ctx context.Context, tenantID string, from, to time.Time) ([]store.FleetHourlyMetric, error)
	GetRobotAnalytics(ctx context.Context, robotID string, from, to time.Time) ([]store.RobotHourlyMetric, error)
	GetAnomalies(ctx context.Context, tenantID string, from, to time.Time) ([]store.RobotAnomaly, error)
}

type analyticsService struct {
	olap  store.AnalyticsStore
	cache store.CacheStore
}

// NewAnalyticsService creates an analytics service with ClickHouse + Redis caching.
func NewAnalyticsService(olap store.AnalyticsStore, cache store.CacheStore) AnalyticsService {
	return &analyticsService{olap: olap, cache: cache}
}

func (s *analyticsService) GetFleetAnalytics(ctx context.Context, tenantID string, from, to time.Time) ([]store.FleetHourlyMetric, error) {
	cacheKey := fmt.Sprintf("analytics:fleet:%s:%s:%s", tenantID, from.Format("2006-01-02"), to.Format("2006-01-02"))

	// Try Redis cache
	cached, err := s.getFromCache(ctx, cacheKey)
	if err == nil {
		var metrics []store.FleetHourlyMetric
		if json.Unmarshal(cached, &metrics) == nil { // corrupt cache → treat as miss, re-query OLAP
			slog.Debug("analytics cache hit", "key", cacheKey)
			return metrics, nil
		}
	}

	// Cache miss → query ClickHouse
	metrics, err := s.olap.GetFleetHourly(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("fleet analytics: %w", err)
	}

	s.setCache(ctx, cacheKey, metrics)
	return metrics, nil
}

func (s *analyticsService) GetRobotAnalytics(ctx context.Context, robotID string, from, to time.Time) ([]store.RobotHourlyMetric, error) {
	cacheKey := fmt.Sprintf("analytics:robot:%s:%s:%s", robotID, from.Format("2006-01-02"), to.Format("2006-01-02"))

	cached, err := s.getFromCache(ctx, cacheKey)
	if err == nil {
		var metrics []store.RobotHourlyMetric
		if json.Unmarshal(cached, &metrics) == nil { // corrupt cache → treat as miss, re-query OLAP
			slog.Debug("analytics cache hit", "key", cacheKey)
			return metrics, nil
		}
	}

	metrics, err := s.olap.GetRobotHourly(ctx, robotID, from, to)
	if err != nil {
		return nil, fmt.Errorf("robot analytics: %w", err)
	}

	s.setCache(ctx, cacheKey, metrics)
	return metrics, nil
}

func (s *analyticsService) GetAnomalies(ctx context.Context, tenantID string, from, to time.Time) ([]store.RobotAnomaly, error) {
	cacheKey := fmt.Sprintf("analytics:anomalies:%s:%s:%s", tenantID, from.Format("2006-01-02"), to.Format("2006-01-02"))

	cached, err := s.getFromCache(ctx, cacheKey)
	if err == nil {
		var anomalies []store.RobotAnomaly
		if json.Unmarshal(cached, &anomalies) == nil {
			return anomalies, nil
		}
	}

	anomalies, err := s.olap.GetAnomalies(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("anomalies: %w", err)
	}

	s.setCache(ctx, cacheKey, anomalies)
	return anomalies, nil
}

func (s *analyticsService) getFromCache(ctx context.Context, key string) ([]byte, error) {
	return s.cache.GetCacheJSON(ctx, key)
}

func (s *analyticsService) setCache(ctx context.Context, key string, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = s.cache.SetCacheJSON(ctx, key, jsonData, analyticsCacheTTL) // best-effort cache
}
