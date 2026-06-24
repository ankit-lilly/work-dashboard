package lambda

import (
	"fmt"
	"strings"
	"time"
)

const (
	TimeoutWarningHigh   = 90.0
	TimeoutWarningMedium = 80.0
	TimeoutHealthy       = 70.0
)

type InvocationRecord struct {
	ExecutionArn string
	StateName    string
	Duration     int64
	TimedOut     bool
	Failed       bool
	Timestamp    time.Time
}

type LambdaResource struct {
	FunctionName string
	FunctionArn  string
	Invocations  []InvocationRecord
	LastSeen     time.Time
}

type LambdaMetrics struct {
	Env             string
	FunctionName    string
	FunctionArn     string
	Timeout         int32
	RecentDuration  int64
	AvgDuration     int64
	MaxDuration     int64
	TimeoutPercent  float64
	MemoryAllocated int32
	MemoryUsed      int32
	MemoryMax       int32
	MemoryRecent    int32
	MemoryPercent   float64
	InvocationCount int
	TimeoutCount    int
	ErrorCount      int
	UsedByStepFuncs []string
	LastInvoked     time.Time
	FirstInvoked    time.Time
	ConfigError     string
}

func ExtractLambdaArnFromResource(resource string) (string, bool) {
	if resource == "" {
		return "", false
	}
	if strings.Contains(resource, ":lambda:") && strings.Contains(resource, ":function:") {
		parts := strings.Split(resource, ":")
		if len(parts) >= 7 {
			return strings.Join(parts[6:], ":"), true
		}
	}
	if !strings.Contains(resource, ":") {
		return resource, true
	}
	return "", false
}

func GetFunctionNameFromArn(arn string) string {
	if arn == "" {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) >= 7 && parts[5] == "function" {
		return strings.Join(parts[6:], ":")
	}
	if !strings.Contains(arn, ":") {
		return arn
	}
	return ""
}

func FormatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	seconds := float64(ms) / 1000.0
	if seconds < 60 {
		return fmt.Sprintf("%.2fs", seconds)
	}
	minutes := int(seconds / 60)
	remainingSeconds := int(seconds) % 60
	return fmt.Sprintf("%dm%ds", minutes, remainingSeconds)
}
