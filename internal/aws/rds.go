package aws

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/pi"
	pitypes "github.com/aws/aws-sdk-go-v2/service/pi/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

// DBInstanceInfo represents RDS instance information
type DBInstanceInfo struct {
	Env                        string
	DBInstanceId               string
	DBResourceId               string // For Performance Insights API
	Engine                     string
	Status                     string
	PerformanceInsightsEnabled bool
}

// QueryMetric represents a query performance metric
type QueryMetric struct {
	QueryText    string
	DBLoad       float64 // Average active sessions
	CallsPerSec  float64
	AvgLatencyMs float64
}

// CPUMetricData represents CPU metrics
type CPUMetricData struct {
	Current    float64
	Average    float64
	Max        float64
	DataPoints []CPUDataPoint
}

// CPUDataPoint represents a single CPU data point
type CPUDataPoint struct {
	Timestamp time.Time
	Value     float64
}

// ConnectionPoolStats represents database connection metrics
type ConnectionPoolStats struct {
	ActiveConnections int
	IdleConnections   int
	MaxConnections    int
	UsedPercent       float64
}

// RDSMetric combines instance info with metrics
type RDSMetric struct {
	Env                        string
	DBInstanceId               string
	DBResourceId               string
	Engine                     string
	Status                     string
	PerformanceInsightsEnabled bool
	TopQueries                 []QueryMetric
	CPUCurrent                 float64
	CPUAverage                 float64
	CPUMax                     float64
	CPUDataPoints              []CPUDataPoint
	ConnectionPool             ConnectionPoolStats
	Error                      string
}

// ListRDSInstances discovers RDS instances with Performance Insights enabled
func (c *Client) ListRDSInstances(ctx context.Context) ([]DBInstanceInfo, error) {
	input := &rds.DescribeDBInstancesInput{}

	var instances []DBInstanceInfo
	paginator := rds.NewDescribeDBInstancesPaginator(c.RDS, input)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			if isPermissionError(err) {
				slog.Warn("insufficient permissions for rds:DescribeDBInstances", "env", c.EnvName, "err", err)
				return nil, fmt.Errorf("permission denied: %w", err)
			}
			return nil, fmt.Errorf("failed to describe DB instances: %w", err)
		}

		for _, db := range output.DBInstances {
			if db.DBInstanceIdentifier == nil {
				continue
			}

			piEnabled := db.PerformanceInsightsEnabled != nil && *db.PerformanceInsightsEnabled

			// Get DbiResourceId for Performance Insights
			resourceId := ""
			if db.DbiResourceId != nil {
				resourceId = *db.DbiResourceId
			}

			engine := ""
			if db.Engine != nil {
				engine = *db.Engine
			}

			status := ""
			if db.DBInstanceStatus != nil {
				status = *db.DBInstanceStatus
			}

			instances = append(instances, DBInstanceInfo{
				Env:                        c.EnvName,
				DBInstanceId:               *db.DBInstanceIdentifier,
				DBResourceId:               resourceId,
				Engine:                     engine,
				Status:                     status,
				PerformanceInsightsEnabled: piEnabled,
			})
		}
	}

	return instances, nil
}

// GetTopQueries retrieves top SQL queries by DB load
func (c *Client) GetTopQueries(ctx context.Context, dbResourceId string, hours int) ([]QueryMetric, error) {
	if dbResourceId == "" {
		return nil, fmt.Errorf("dbResourceId is required")
	}

	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(hours) * time.Hour)

	input := &pi.DescribeDimensionKeysInput{
		ServiceType: pitypes.ServiceTypeRds,
		Identifier:  aws.String(dbResourceId),
		StartTime:   aws.Time(startTime),
		EndTime:     aws.Time(endTime),
		Metric:      aws.String("db.load.avg"),
		GroupBy: &pitypes.DimensionGroup{
			Group: aws.String("db.sql"),
			Limit: aws.Int32(10), // Top 10 queries
		},
	}

	output, err := c.PI.DescribeDimensionKeys(ctx, input)
	if err != nil {
		if isPermissionError(err) {
			slog.Warn("insufficient permissions for pi:DescribeDimensionKeys", "env", c.EnvName, "err", err)
			return nil, fmt.Errorf("permission denied: %w", err)
		}
		return nil, fmt.Errorf("failed to describe dimension keys: %w", err)
	}

	var queries []QueryMetric
	for _, key := range output.Keys {
		if key.Dimensions == nil {
			continue
		}

		queryText := ""
		if sqlText, ok := key.Dimensions["db.sql.statement"]; ok {
			queryText = sqlText
		}

		dbLoad := 0.0
		if key.Total != nil {
			dbLoad = *key.Total
		}

		// Get additional metrics for this query
		callsPerSec := 0.0
		avgLatency := 0.0

		// Note: Additional metrics like calls/sec and latency would typically
		// come from GetResourceMetrics with specific dimension keys
		// For now, we'll use placeholder values as these require additional API calls

		queries = append(queries, QueryMetric{
			QueryText:    queryText,
			DBLoad:       dbLoad,
			CallsPerSec:  callsPerSec,
			AvgLatencyMs: avgLatency,
		})
	}

	return queries, nil
}

// GetCPUMetrics retrieves CPU metrics for the specified resource
func (c *Client) GetCPUMetrics(ctx context.Context, dbResourceId string, hours int) (*CPUMetricData, error) {
	if dbResourceId == "" {
		return nil, fmt.Errorf("dbResourceId is required")
	}

	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(hours) * time.Hour)

	input := &pi.GetResourceMetricsInput{
		ServiceType:     pitypes.ServiceTypeRds,
		Identifier:      aws.String(dbResourceId),
		StartTime:       aws.Time(startTime),
		EndTime:         aws.Time(endTime),
		PeriodInSeconds: aws.Int32(300), // 5 minute intervals
		MetricQueries: []pitypes.MetricQuery{
			{
				Metric: aws.String("os.cpuUtilization.total.avg"),
			},
		},
	}

	output, err := c.PI.GetResourceMetrics(ctx, input)
	if err != nil {
		if isPermissionError(err) {
			slog.Warn("insufficient permissions for pi:GetResourceMetrics", "env", c.EnvName, "err", err)
			return nil, fmt.Errorf("permission denied: %w", err)
		}
		return nil, fmt.Errorf("failed to get resource metrics: %w", err)
	}

	cpuData := &CPUMetricData{
		DataPoints: []CPUDataPoint{},
	}

	if len(output.MetricList) == 0 {
		return cpuData, nil
	}

	var sum float64
	var max float64
	var current float64
	count := 0

	for _, metric := range output.MetricList {
		if metric.DataPoints == nil {
			continue
		}

		for _, dp := range metric.DataPoints {
			if dp.Timestamp == nil || dp.Value == nil {
				continue
			}

			value := *dp.Value
			timestamp := *dp.Timestamp

			cpuData.DataPoints = append(cpuData.DataPoints, CPUDataPoint{
				Timestamp: timestamp,
				Value:     value,
			})

			sum += value
			count++

			if value > max {
				max = value
			}

			// Current is the most recent value
			if timestamp.After(endTime.Add(-10 * time.Minute)) {
				current = value
			}
		}
	}

	if count > 0 {
		cpuData.Average = sum / float64(count)
	}
	cpuData.Max = max
	cpuData.Current = current

	return cpuData, nil
}

// GetConnectionPoolStats retrieves connection pool metrics from CloudWatch
func (c *Client) GetConnectionPoolStats(ctx context.Context, dbInstanceId string) (ConnectionPoolStats, error) {
	stats := ConnectionPoolStats{}

	endTime := time.Now()
	startTime := endTime.Add(-10 * time.Minute) // Last 10 minutes

	// Get DatabaseConnections metric from CloudWatch
	input := &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/RDS"),
		MetricName: aws.String("DatabaseConnections"),
		Dimensions: []cwtypes.Dimension{
			{
				Name:  aws.String("DBInstanceIdentifier"),
				Value: aws.String(dbInstanceId),
			},
		},
		StartTime: aws.Time(startTime),
		EndTime:   aws.Time(endTime),
		Period:    aws.Int32(300), // 5 minute periods
		Statistics: []cwtypes.Statistic{
			cwtypes.StatisticAverage,
			cwtypes.StatisticMaximum,
		},
	}

	output, err := c.CW.GetMetricStatistics(ctx, input)
	if err != nil {
		return stats, fmt.Errorf("failed to get connection metrics: %w", err)
	}

	// Extract the most recent data point
	if len(output.Datapoints) > 0 {
		// Sort by timestamp to get latest
		latestPoint := output.Datapoints[0]
		for _, dp := range output.Datapoints {
			if dp.Timestamp.After(*latestPoint.Timestamp) {
				latestPoint = dp
			}
		}

		if latestPoint.Average != nil {
			stats.ActiveConnections = int(*latestPoint.Average)
		}
	}

	// Set reasonable defaults for max connections based on instance type
	// These are typical values - actual may vary based on parameter group settings
	stats.MaxConnections = 100 // Conservative default

	// If we have active connections, we can make a better estimate
	if stats.ActiveConnections > 0 {
		// Ensure max is at least higher than current
		if stats.ActiveConnections >= stats.MaxConnections {
			stats.MaxConnections = stats.ActiveConnections + 50
		}
	}

	// Calculate percentage
	if stats.MaxConnections > 0 {
		stats.UsedPercent = float64(stats.ActiveConnections) / float64(stats.MaxConnections) * 100
	}

	// Estimate idle connections (rough approximation - typically 20-40% of connections are idle)
	// Without direct database access, we can't get exact numbers
	if stats.ActiveConnections > 5 {
		stats.IdleConnections = int(float64(stats.ActiveConnections) * 0.3)
	}

	return stats, nil
}

// GetRDSMetrics retrieves comprehensive RDS metrics for all instances
func (c *Client) GetRDSMetrics(ctx context.Context, hours int, maxQueries int) ([]RDSMetric, error) {
	instances, err := c.ListRDSInstances(ctx)
	if err != nil {
		return nil, err
	}

	var metrics []RDSMetric
	for _, instance := range instances {
		metric := RDSMetric{
			Env:                        instance.Env,
			DBInstanceId:               instance.DBInstanceId,
			DBResourceId:               instance.DBResourceId,
			Engine:                     instance.Engine,
			Status:                     instance.Status,
			PerformanceInsightsEnabled: instance.PerformanceInsightsEnabled,
		}

		if !instance.PerformanceInsightsEnabled {
			metric.Error = "Performance Insights not enabled"
			metrics = append(metrics, metric)
			continue
		}

		// Get top queries
		queries, err := c.GetTopQueries(ctx, instance.DBResourceId, hours)
		if err != nil {
			metric.Error = fmt.Sprintf("Failed to get queries: %v", err)
			slog.Warn("failed to get top queries", "env", c.EnvName, "db", instance.DBInstanceId, "err", err)
		} else {
			// Limit to maxQueries
			if len(queries) > maxQueries {
				queries = queries[:maxQueries]
			}
			metric.TopQueries = queries
		}

		// Get CPU metrics
		cpuData, err := c.GetCPUMetrics(ctx, instance.DBResourceId, hours)
		if err != nil {
			slog.Warn("failed to get CPU metrics", "env", c.EnvName, "db", instance.DBInstanceId, "err", err)
		} else {
			metric.CPUCurrent = cpuData.Current
			metric.CPUAverage = cpuData.Average
			metric.CPUMax = cpuData.Max
			metric.CPUDataPoints = cpuData.DataPoints
		}

		// Get connection pool stats
		connStats, err := c.GetConnectionPoolStats(ctx, instance.DBInstanceId)
		if err != nil {
			slog.Warn("failed to get connection pool stats", "env", c.EnvName, "db", instance.DBInstanceId, "err", err)
		} else {
			metric.ConnectionPool = connStats
		}

		metrics = append(metrics, metric)
	}

	return metrics, nil
}

// isPermissionError checks if an error is a permission-related error
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "AccessDenied") ||
		strings.Contains(errStr, "UnauthorizedOperation") ||
		strings.Contains(errStr, "AccessDeniedException") ||
		strings.Contains(errStr, "UnauthorizedException")
}

// roundFloat rounds a float to n decimal places
func roundFloat(val float64, precision int) float64 {
	ratio := math.Pow(10, float64(precision))
	return math.Round(val*ratio) / ratio
}
