package rds

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/config"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
)

type RDSRepository interface {
	GetAllMetrics(ctx context.Context, hours int, maxQueries int) ([]domain_rds.RDSMetric, error)
}

type Service struct {
	repos map[string]RDSRepository
	cfg   *config.Config
}

func NewService(repos map[string]RDSRepository, cfg *config.Config, _ func() int) *Service {
	return &Service{
		repos: repos,
		cfg:   cfg,
	}
}

func (s *Service) FetchMetrics() ([]domain_rds.RDSMetric, error) {
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

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		metrics, err := repo.GetAllMetrics(ctx, metricHours, maxQueries)
		cancel()
		if err != nil {
			slog.Warn("failed to get RDS metrics", "env", env, "err", err)
			continue
		}

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
