package aws

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
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
	DBInstanceClass            string // Instance type (e.g., db.t3.small)
	Engine                     string
	Status                     string
	PerformanceInsightsEnabled bool
	DBParameterGroupName       string // Parameter group name for max_connections lookup
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

			instanceClass := ""
			if db.DBInstanceClass != nil {
				instanceClass = *db.DBInstanceClass
			}

			// Get parameter group name
			paramGroupName := ""
			if len(db.DBParameterGroups) > 0 && db.DBParameterGroups[0].DBParameterGroupName != nil {
				paramGroupName = *db.DBParameterGroups[0].DBParameterGroupName
			}

			instances = append(instances, DBInstanceInfo{
				Env:                        c.EnvName,
				DBInstanceId:               *db.DBInstanceIdentifier,
				DBResourceId:               resourceId,
				DBInstanceClass:            instanceClass,
				Engine:                     engine,
				Status:                     status,
				PerformanceInsightsEnabled: piEnabled,
				DBParameterGroupName:       paramGroupName,
			})
		}
	}

	return instances, nil
}

// GetMaxConnectionsFromParameterGroup retrieves the actual max_connections value from RDS parameter group
func (c *Client) GetMaxConnectionsFromParameterGroup(ctx context.Context, parameterGroupName string) (int, error) {
	if parameterGroupName == "" {
		return 0, fmt.Errorf("parameter group name is required")
	}

	input := &rds.DescribeDBParametersInput{
		DBParameterGroupName: aws.String(parameterGroupName),
		Source:               aws.String("user"), // Get user-modified values, falls back to system defaults
	}

	// max_connections parameter name varies by engine
	maxConnParam := "max_connections"

	paginator := rds.NewDescribeDBParametersPaginator(c.RDS, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			if isPermissionError(err) {
				slog.Warn("insufficient permissions for rds:DescribeDBParameters", "env", c.EnvName, "err", err)
				return 0, fmt.Errorf("permission denied: %w", err)
			}
			return 0, fmt.Errorf("failed to describe parameters: %w", err)
		}

		for _, param := range output.Parameters {
			if param.ParameterName != nil && *param.ParameterName == maxConnParam {
				if param.ParameterValue != nil {
					// Parse the value
					var maxConn int
					_, err := fmt.Sscanf(*param.ParameterValue, "%d", &maxConn)
					if err == nil && maxConn > 0 {
						return maxConn, nil
					}

					// Handle formula-based values like {DBInstanceClassMemory/9531392}
					// Extract the formula and evaluate if needed
					if strings.Contains(*param.ParameterValue, "DBInstanceClassMemory") {
						// This is a formula, we can't evaluate it without instance memory
						// Return 0 to signal we need to use fallback
						return 0, fmt.Errorf("parameter uses formula: %s", *param.ParameterValue)
					}
				}
			}
		}
	}

	// Parameter not found or not customized
	return 0, fmt.Errorf("max_connections parameter not found in parameter group")
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

// estimateMaxConnections estimates max_connections based on RDS instance class and engine
// PostgreSQL formula: LEAST({DBInstanceClassMemory/9531392}, 5000)
// MySQL formula: LEAST({DBInstanceClassMemory/12582880}, 16000)
// These are approximate values based on AWS documentation
func estimateMaxConnections(instanceClass, engine string) int {
	// Determine which formula to use based on engine
	isMySQL := strings.Contains(strings.ToLower(engine), "mysql") ||
		strings.Contains(strings.ToLower(engine), "mariadb")

	// Map of common instance classes to memory in MB
	memoryMap := map[string]int{
		// T3 instances (burstable)
		"db.t3.micro":   1024,  // 1 GB
		"db.t3.small":   2048,  // 2 GB
		"db.t3.medium":  4096,  // 4 GB
		"db.t3.large":   8192,  // 8 GB
		"db.t3.xlarge":  16384, // 16 GB
		"db.t3.2xlarge": 32768, // 32 GB

		// T4g instances (ARM burstable)
		"db.t4g.micro":   1024,
		"db.t4g.small":   2048,
		"db.t4g.medium":  4096,
		"db.t4g.large":   8192,
		"db.t4g.xlarge":  16384,
		"db.t4g.2xlarge": 32768,

		// M5 instances (general purpose)
		"db.m5.large":    8192,   // 8 GB
		"db.m5.xlarge":   16384,  // 16 GB
		"db.m5.2xlarge":  32768,  // 32 GB
		"db.m5.4xlarge":  65536,  // 64 GB
		"db.m5.8xlarge":  131072, // 128 GB
		"db.m5.12xlarge": 196608, // 192 GB
		"db.m5.16xlarge": 262144, // 256 GB
		"db.m5.24xlarge": 393216, // 384 GB

		// M6g instances (ARM general purpose)
		"db.m6g.large":    8192,
		"db.m6g.xlarge":   16384,
		"db.m6g.2xlarge":  32768,
		"db.m6g.4xlarge":  65536,
		"db.m6g.8xlarge":  131072,
		"db.m6g.12xlarge": 196608,
		"db.m6g.16xlarge": 262144,

		// R5 instances (memory optimized)
		"db.r5.large":    16384,  // 16 GB
		"db.r5.xlarge":   32768,  // 32 GB
		"db.r5.2xlarge":  65536,  // 64 GB
		"db.r5.4xlarge":  131072, // 128 GB
		"db.r5.8xlarge":  262144, // 256 GB
		"db.r5.12xlarge": 393216, // 384 GB
		"db.r5.16xlarge": 524288, // 512 GB
		"db.r5.24xlarge": 786432, // 768 GB

		// R6g instances (ARM memory optimized)
		"db.r6g.large":    16384,
		"db.r6g.xlarge":   32768,
		"db.r6g.2xlarge":  65536,
		"db.r6g.4xlarge":  131072,
		"db.r6g.8xlarge":  262144,
		"db.r6g.12xlarge": 393216,
		"db.r6g.16xlarge": 524288,
	}

	memoryMB, ok := memoryMap[instanceClass]
	if !ok {
		// Default fallback for unknown instances
		return 200
	}

	// Convert MB to bytes
	memoryBytes := int64(memoryMB) * 1024 * 1024

	var maxConn int
	if isMySQL {
		// MySQL/MariaDB formula
		maxConn = int(memoryBytes / 12582880)
		if maxConn > 16000 {
			maxConn = 16000
		}
	} else {
		// PostgreSQL formula (default)
		maxConn = int(memoryBytes / 9531392)
		if maxConn > 5000 {
			maxConn = 5000
		}
	}

	return maxConn
}

// GetConnectionPoolStats retrieves connection pool metrics from CloudWatch
func (c *Client) GetConnectionPoolStats(ctx context.Context, dbInstanceId, instanceClass, engine, paramGroupName string) (ConnectionPoolStats, error) {
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

	// Try to get actual max_connections from parameter group first
	maxConn := 0
	if paramGroupName != "" {
		maxConn, err = c.GetMaxConnectionsFromParameterGroup(ctx, paramGroupName)
		if err != nil {
			// Log but don't fail - we'll fall back to estimation
			slog.Debug("failed to get max_connections from parameter group, using estimation",
				"env", c.EnvName,
				"db", dbInstanceId,
				"param_group", paramGroupName,
				"err", err)
		}
	}

	// Fall back to engine-specific formula estimation if API call failed
	if maxConn == 0 {
		maxConn = estimateMaxConnections(instanceClass, engine)
		slog.Debug("using estimated max_connections",
			"env", c.EnvName,
			"db", dbInstanceId,
			"instance_class", instanceClass,
			"engine", engine,
			"estimated", maxConn)
	} else {
		slog.Info("retrieved actual max_connections from parameter group",
			"env", c.EnvName,
			"db", dbInstanceId,
			"param_group", paramGroupName,
			"max_connections", maxConn)
	}

	stats.MaxConnections = maxConn

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

// GetRDSMetrics retrieves comprehensive RDS metrics for all instances in parallel
func (c *Client) GetRDSMetrics(ctx context.Context, hours int, maxQueries int) ([]RDSMetric, error) {
	instances, err := c.ListRDSInstances(ctx)
	if err != nil {
		return nil, err
	}

	// Fetch metrics for each instance in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	metrics := make([]RDSMetric, 0, len(instances))

	for _, instance := range instances {
		wg.Add(1)
		go func(inst DBInstanceInfo) {
			defer wg.Done()

			metric := RDSMetric{
				Env:                        inst.Env,
				DBInstanceId:               inst.DBInstanceId,
				DBResourceId:               inst.DBResourceId,
				Engine:                     inst.Engine,
				Status:                     inst.Status,
				PerformanceInsightsEnabled: inst.PerformanceInsightsEnabled,
			}

			if !inst.PerformanceInsightsEnabled {
				metric.Error = "Performance Insights not enabled"
				mu.Lock()
				metrics = append(metrics, metric)
				mu.Unlock()
				return
			}

			// Get top queries
			queries, err := c.GetTopQueries(ctx, inst.DBResourceId, hours)
			if err != nil {
				metric.Error = fmt.Sprintf("Failed to get queries: %v", err)
				slog.Warn("failed to get top queries", "env", c.EnvName, "db", inst.DBInstanceId, "err", err)
			} else {
				// Limit to maxQueries
				if len(queries) > maxQueries {
					queries = queries[:maxQueries]
				}
				metric.TopQueries = queries
			}

			// Get CPU metrics
			cpuData, err := c.GetCPUMetrics(ctx, inst.DBResourceId, hours)
			if err != nil {
				slog.Warn("failed to get CPU metrics", "env", c.EnvName, "db", inst.DBInstanceId, "err", err)
			} else {
				metric.CPUCurrent = cpuData.Current
				metric.CPUAverage = cpuData.Average
				metric.CPUMax = cpuData.Max
				metric.CPUDataPoints = cpuData.DataPoints
			}

			// Get connection pool stats
			connStats, err := c.GetConnectionPoolStats(ctx, inst.DBInstanceId, inst.DBInstanceClass, inst.Engine, inst.DBParameterGroupName)
			if err != nil {
				slog.Warn("failed to get connection pool stats", "env", c.EnvName, "db", inst.DBInstanceId, "err", err)
			} else {
				metric.ConnectionPool = connStats
			}

			mu.Lock()
			metrics = append(metrics, metric)
			mu.Unlock()
		}(instance)
	}

	wg.Wait()

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
