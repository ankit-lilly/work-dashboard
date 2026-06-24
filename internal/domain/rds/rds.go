package rds

import (
	"strings"
	"time"
)

type DBInstanceInfo struct {
	Env                        string
	DBInstanceId               string
	DBResourceId               string
	DBInstanceClass            string
	Engine                     string
	Status                     string
	PerformanceInsightsEnabled bool
	DBParameterGroupName       string
}

type QueryMetric struct {
	QueryText    string
	DBLoad       float64
	CallsPerSec  float64
	AvgLatencyMs float64
}

type CPUDataPoint struct {
	Timestamp time.Time
	Value     float64
}

type ConnectionPoolStats struct {
	ActiveConnections    int
	IdleConnections      int
	MaxConnections       int
	UsedPercent          float64
	MaxConnectionsSource string
}

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

func EstimateMaxConnections(instanceClass, engine string) int {
	isMySQL := strings.Contains(strings.ToLower(engine), "mysql") ||
		strings.Contains(strings.ToLower(engine), "mariadb")

	memoryMap := map[string]int{
		"db.t3.micro":     1024,
		"db.t3.small":     2048,
		"db.t3.medium":    4096,
		"db.t3.large":     8192,
		"db.t3.xlarge":    16384,
		"db.t3.2xlarge":   32768,
		"db.t4g.micro":    1024,
		"db.t4g.small":    2048,
		"db.t4g.medium":   4096,
		"db.t4g.large":    8192,
		"db.t4g.xlarge":   16384,
		"db.t4g.2xlarge":  32768,
		"db.m5.large":     8192,
		"db.m5.xlarge":    16384,
		"db.m5.2xlarge":   32768,
		"db.m5.4xlarge":   65536,
		"db.m5.8xlarge":   131072,
		"db.m5.12xlarge":  196608,
		"db.m5.16xlarge":  262144,
		"db.m5.24xlarge":  393216,
		"db.m6g.large":    8192,
		"db.m6g.xlarge":   16384,
		"db.m6g.2xlarge":  32768,
		"db.m6g.4xlarge":  65536,
		"db.m6g.8xlarge":  131072,
		"db.m6g.12xlarge": 196608,
		"db.m6g.16xlarge": 262144,
		"db.r5.large":     16384,
		"db.r5.xlarge":    32768,
		"db.r5.2xlarge":   65536,
		"db.r5.4xlarge":   131072,
		"db.r5.8xlarge":   262144,
		"db.r5.12xlarge":  393216,
		"db.r5.16xlarge":  524288,
		"db.r5.24xlarge":  786432,
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
		return 200
	}

	memoryBytes := int64(memoryMB) * 1024 * 1024
	if isMySQL {
		maxConn := int(memoryBytes / 12582880)
		if maxConn > 16000 {
			maxConn = 16000
		}
		return maxConn
	}

	maxConn := int(memoryBytes / 9531392)
	if maxConn > 5000 {
		maxConn = 5000
	}
	return maxConn
}

func IsRelevantEnv(env string) bool {
	return strings.Contains(strings.ToLower(env), "camp")
}
