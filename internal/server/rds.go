package server

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/starfederation/datastar-go/datastar"
)

// fetchRDSMetrics fetches RDS metrics with adaptive caching based on active executions
func (s *Server) fetchRDSMetrics() ([]aws.RDSMetric, error) {
	// Determine interval based on active executions state
	hasActiveExecs := s.getActiveExecutionsCount() > 0

	slowInterval := 30 * time.Minute
	fastInterval := 30 * time.Second
	if s.cfg != nil {
		slowInterval = s.cfg.Polling.RDSSlowInterval
		fastInterval = s.cfg.Polling.RDSFastInterval
	}

	interval := slowInterval
	if hasActiveExecs {
		interval = fastInterval
	}

	metricHours := 2
	maxQueries := 10
	if s.cfg != nil {
		metricHours = s.cfg.Limits.RDSMetricHours
		maxQueries = s.cfg.Limits.RDSMaxQueries
	}

	// Aggregate metrics from all environments with per-environment caching
	// Only fetch RDS metrics for CAMP environments (DSOA doesn't use RDS)
	var allMetrics []aws.RDSMetric

	for _, client := range s.awsManager.Clients {
		// Skip non-CAMP environments (DSOA doesn't have RDS)
		if !strings.Contains(strings.ToLower(client.EnvName), "camp") {
			slog.Debug("skipping RDS metrics for non-CAMP environment", "env", client.EnvName)
			continue
		}

		now := time.Now()
		s.rdsCacheMu.Lock()
		last := s.rdsCacheAt[client.EnvName]

		// If cached within interval, skip fetch and use cached data
		if !last.IsZero() && now.Sub(last) < interval {
			// Use cached metrics
			if cached, ok := s.rdsCache[client.EnvName]; ok {
				allMetrics = append(allMetrics, cached...)
				s.rdsCacheMu.Unlock()
				slog.Debug("using cached RDS metrics", "env", client.EnvName, "cached_at", last, "interval", interval, "has_active", hasActiveExecs)
				continue
			}
		}
		s.rdsCacheMu.Unlock()

		// Fetch fresh data (now parallelized within GetRDSMetrics)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		metrics, err := client.GetRDSMetrics(ctx, metricHours, maxQueries)
		cancel()

		if err != nil {
			slog.Warn("failed to get RDS metrics", "env", client.EnvName, "err", err)
			// Use stale cached data if available
			s.rdsCacheMu.Lock()
			if cached, ok := s.rdsCache[client.EnvName]; ok {
				allMetrics = append(allMetrics, cached...)
				slog.Info("using stale RDS cache due to error", "env", client.EnvName, "err", err)
			}
			s.rdsCacheMu.Unlock()
			continue
		}

		// Update cache
		s.rdsCacheMu.Lock()
		s.rdsCache[client.EnvName] = metrics
		s.rdsCacheAt[client.EnvName] = now
		s.rdsCacheMu.Unlock()

		allMetrics = append(allMetrics, metrics...)
		slog.Info("fetched RDS metrics", "env", client.EnvName, "db_count", len(metrics), "interval", interval, "has_active", hasActiveExecs)
	}

	return allMetrics, nil
}

// handleRDSUpdates handles SSE updates for RDS Performance Insights
func (s *Server) handleRDSUpdates(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ctx := r.Context()

	rdsCh := s.rdsBroadcaster.Subscribe()
	defer s.rdsBroadcaster.Unsubscribe(rdsCh)

	// Send initial loading state
	sse.PatchSignals([]byte(`{"rds_loading": true, "rds_db_count": 0}`))

	for {
		select {
		case <-ctx.Done():
			return
		case allMetrics, ok := <-rdsCh:
			if !ok {
				return
			}

			// Update loading state and count
			sse.PatchSignals([]byte(fmt.Sprintf(`{"rds_loading": false, "rds_db_count": %d}`, len(allMetrics))))

			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}

			metricHours := 2
			if s.cfg != nil {
				metricHours = s.cfg.Limits.RDSMetricHours
			}

			err := tmpl.ExecuteTemplate(&buf, "rds-metrics", map[string]any{
				"Metrics":     allMetrics,
				"MetricHours": metricHours,
			})
			if err != nil {
				slog.Error("template render failed", "template", "rds-metrics", "error", err)
				return
			}

			sse.PatchElements(
				buf.String(),
				datastar.WithSelector("#rds-metrics-content"),
				datastar.WithMode(datastar.ElementPatchModeInner),
				datastar.WithUseViewTransitions(false),
			)
		}
	}
}
