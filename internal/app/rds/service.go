package rds

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/config"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
)

type RDSRepository interface {
	GetAllMetrics(ctx context.Context, hours int, maxQueries int) ([]domain_rds.RDSMetric, error)
}

type Service struct {
	repos       map[string]RDSRepository
	cfg         *config.Config
	activeCount func() int
	cacheMu     sync.Mutex
	cache       map[string][]domain_rds.RDSMetric
	cacheAt     map[string]time.Time
}

func NewService(repos map[string]RDSRepository, cfg *config.Config, activeCount func() int) *Service {
	return &Service{
		repos:       repos,
		cfg:         cfg,
		activeCount: activeCount,
		cache:       make(map[string][]domain_rds.RDSMetric),
		cacheAt:     make(map[string]time.Time),
	}
}

func (s *Service) FetchMetrics() ([]domain_rds.RDSMetric, error) {
	hasActive := s.activeCount != nil && s.activeCount() > 0

	slowInterval := 30 * time.Minute
	fastInterval := 30 * time.Second
	if s.cfg != nil {
		slowInterval = s.cfg.Polling.RDSSlowInterval
		fastInterval = s.cfg.Polling.RDSFastInterval
	}

	interval := slowInterval
	if hasActive {
		interval = fastInterval
	}

	metricHours := 2
	maxQueries := 10
	if s.cfg != nil {
		metricHours = s.cfg.Limits.RDSMetricHours
		maxQueries = s.cfg.Limits.RDSMaxQueries
	}

	var allMetrics []domain_rds.RDSMetric
	for env, repo := range s.repos {
		if !domain_rds.IsRelevantEnv(env) {
			slog.Debug("skipping RDS metrics for non-CAMP environment", "env", env)
			continue
		}

		now := time.Now()
		s.cacheMu.Lock()
		last := s.cacheAt[env]
		if !last.IsZero() && now.Sub(last) < interval {
			if cached, ok := s.cache[env]; ok {
				allMetrics = append(allMetrics, cached...)
				s.cacheMu.Unlock()
				continue
			}
		}
		s.cacheMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		metrics, err := repo.GetAllMetrics(ctx, metricHours, maxQueries)
		cancel()
		if err != nil {
			slog.Warn("failed to get RDS metrics", "env", env, "err", err)
			s.cacheMu.Lock()
			if cached, ok := s.cache[env]; ok {
				allMetrics = append(allMetrics, cached...)
			}
			s.cacheMu.Unlock()
			continue
		}

		s.cacheMu.Lock()
		s.cache[env] = metrics
		s.cacheAt[env] = now
		s.cacheMu.Unlock()

		allMetrics = append(allMetrics, metrics...)
	}

	sort.Slice(allMetrics, func(i, j int) bool {
		if allMetrics[i].Env != allMetrics[j].Env {
			return allMetrics[i].Env < allMetrics[j].Env
		}
		return allMetrics[i].DBInstanceId < allMetrics[j].DBInstanceId
	})

	return allMetrics, nil
}

func (s *Service) CachedMetrics() []domain_rds.RDSMetric {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	var allMetrics []domain_rds.RDSMetric
	for _, cached := range s.cache {
		allMetrics = append(allMetrics, cached...)
	}
	sort.Slice(allMetrics, func(i, j int) bool {
		if allMetrics[i].Env != allMetrics[j].Env {
			return allMetrics[i].Env < allMetrics[j].Env
		}
		return allMetrics[i].DBInstanceId < allMetrics[j].DBInstanceId
	})
	return allMetrics
}
