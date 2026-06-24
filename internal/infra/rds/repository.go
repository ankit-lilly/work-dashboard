package rds

import (
	"context"

	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
	"github.com/EliLillyCo/work-dashboard/internal/infra/awsclient"
)

type Repository struct {
	client *awsclient.AWSClient
}

func NewRepository(client *awsclient.AWSClient) *Repository {
	return &Repository{client: client}
}

func (r *Repository) GetAllMetrics(ctx context.Context, hours int, maxQueries int) ([]domain_rds.RDSMetric, error) {
	metrics, err := r.client.GetRDSMetrics(ctx, hours, maxQueries)
	if err != nil {
		return nil, err
	}

	out := make([]domain_rds.RDSMetric, 0, len(metrics))
	for _, metric := range metrics {
		queries := make([]domain_rds.QueryMetric, 0, len(metric.TopQueries))
		for _, query := range metric.TopQueries {
			queries = append(queries, domain_rds.QueryMetric{
				QueryText:    query.QueryText,
				DBLoad:       query.DBLoad,
				CallsPerSec:  query.CallsPerSec,
				AvgLatencyMs: query.AvgLatencyMs,
			})
		}

		points := make([]domain_rds.CPUDataPoint, 0, len(metric.CPUDataPoints))
		for _, point := range metric.CPUDataPoints {
			points = append(points, domain_rds.CPUDataPoint{
				Timestamp: point.Timestamp,
				Value:     point.Value,
			})
		}

		out = append(out, domain_rds.RDSMetric{
			Env:                        metric.Env,
			DBInstanceId:               metric.DBInstanceId,
			DBResourceId:               metric.DBResourceId,
			Engine:                     metric.Engine,
			Status:                     metric.Status,
			PerformanceInsightsEnabled: metric.PerformanceInsightsEnabled,
			TopQueries:                 queries,
			CPUCurrent:                 metric.CPUCurrent,
			CPUAverage:                 metric.CPUAverage,
			CPUMax:                     metric.CPUMax,
			CPUDataPoints:              points,
			ConnectionPool: domain_rds.ConnectionPoolStats{
				ActiveConnections:    metric.ConnectionPool.ActiveConnections,
				IdleConnections:      metric.ConnectionPool.IdleConnections,
				MaxConnections:       metric.ConnectionPool.MaxConnections,
				UsedPercent:          metric.ConnectionPool.UsedPercent,
				MaxConnectionsSource: metric.ConnectionPool.MaxConnectionsSource,
			},
			Error: metric.Error,
		})
	}

	return out, nil
}
