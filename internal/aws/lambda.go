package aws

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

const (
	TimeoutWarningHigh   = 90.0 // Red warning
	TimeoutWarningMedium = 80.0 // Yellow warning
	TimeoutHealthy       = 70.0 // Green
)

// InvocationRecord tracks a single Lambda invocation from execution history
type InvocationRecord struct {
	ExecutionArn string
	StateName    string
	Duration     int64 // milliseconds
	TimedOut     bool
	Failed       bool
	Timestamp    time.Time
}

// LambdaResource represents discovered Lambda function usage
type LambdaResource struct {
	FunctionName string
	FunctionArn  string
	Invocations  []InvocationRecord
	LastSeen     time.Time
}

// LambdaMetrics contains performance metrics for a Lambda function
type LambdaMetrics struct {
	Env             string
	FunctionName    string
	FunctionArn     string
	Timeout         int32   // seconds
	RecentDuration  int64   // milliseconds (most recent)
	AvgDuration     int64   // milliseconds (average across invocations)
	MaxDuration     int64   // milliseconds (max duration)
	TimeoutPercent  float64 // percentage of timeout limit
	MemoryAllocated int32   // MB
	MemoryUsed      int32   // MB (average)
	MemoryMax       int32   // MB (maximum observed)
	MemoryRecent    int32   // MB (most recent invocation)
	MemoryPercent   float64 // percentage of allocated memory used (based on average)
	InvocationCount int
	TimeoutCount    int
	ErrorCount      int
	UsedByStepFuncs []string // Which Step Functions use this Lambda
	LastInvoked     time.Time
	FirstInvoked    time.Time // Earliest invocation in the data set
	ConfigError     string    // Error fetching configuration
}

// GetLambdaMetrics fetches Lambda configuration and calculates timeout metrics
// Uses CloudWatch Logs Insights to query the last 20 invocations directly
func (c *Client) GetLambdaMetrics(ctx context.Context, functionName string, usedByStepFuncs []string) (*LambdaMetrics, error) {
	// Get function configuration for timeout and memory
	config, err := c.Lambda.GetFunctionConfiguration(ctx, &lambda.GetFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		slog.Warn("failed to get lambda configuration", "env", c.EnvName, "function", functionName, "error", err)
		// Return partial metrics with error
		return &LambdaMetrics{
			Env:             c.EnvName,
			FunctionName:    functionName,
			ConfigError:     err.Error(),
			InvocationCount: 0,
			UsedByStepFuncs: usedByStepFuncs,
		}, nil
	}

	timeout := aws.ToInt32(config.Timeout)
	memoryAllocated := aws.ToInt32(config.MemorySize)
	functionArn := aws.ToString(config.FunctionArn)

	// Get last 20 invocations from CloudWatch Logs
	invocations, err := c.GetLast20Invocations(ctx, functionName)
	if err != nil || len(invocations) == 0 {
		// Return basic metrics with no invocation data
		slog.Debug("no invocation data from logs", "env", c.EnvName, "function", functionName, "error", err)
		return &LambdaMetrics{
			Env:             c.EnvName,
			FunctionName:    functionName,
			FunctionArn:     functionArn,
			Timeout:         timeout,
			MemoryAllocated: memoryAllocated,
			InvocationCount: 0,
			UsedByStepFuncs: usedByStepFuncs,
		}, nil
	}

	// Calculate metrics from CloudWatch Logs invocations
	var totalDuration int64
	var maxDuration int64
	var recentDuration int64
	var totalMemory int64
	var maxMemory int32
	var recentMemory int32
	var lastInvoked time.Time
	var firstInvoked time.Time

	for i, inv := range invocations {
		// Duration metrics
		totalDuration += inv.Duration
		if inv.Duration > maxDuration {
			maxDuration = inv.Duration
		}

		// Memory metrics
		totalMemory += int64(inv.MemoryUsed)
		if inv.MemoryUsed > maxMemory {
			maxMemory = inv.MemoryUsed
		}

		// Most recent = first in desc order (CloudWatch returns desc by timestamp)
		if i == 0 {
			recentDuration = inv.Duration
			recentMemory = inv.MemoryUsed
			lastInvoked = inv.Timestamp
		}

		// Oldest = last in desc order
		if i == len(invocations)-1 {
			firstInvoked = inv.Timestamp
		}
	}

	avgDuration := int64(0)
	avgMemory := int32(0)
	if len(invocations) > 0 {
		avgDuration = totalDuration / int64(len(invocations))
		avgMemory = int32(totalMemory / int64(len(invocations)))
	}

	// Calculate timeout percentage based on average duration
	timeoutMillis := int64(timeout) * 1000
	timeoutPercent := 0.0
	if timeoutMillis > 0 {
		timeoutPercent = (float64(avgDuration) / float64(timeoutMillis)) * 100
	}

	// Calculate memory percentage
	memoryPercent := 0.0
	if memoryAllocated > 0 && avgMemory > 0 {
		memoryPercent = (float64(avgMemory) / float64(memoryAllocated)) * 100
	}

	metrics := &LambdaMetrics{
		Env:             c.EnvName,
		FunctionName:    functionName,
		FunctionArn:     functionArn,
		Timeout:         timeout,
		RecentDuration:  recentDuration,
		AvgDuration:     avgDuration,
		MaxDuration:     maxDuration,
		TimeoutPercent:  timeoutPercent,
		MemoryAllocated: memoryAllocated,
		MemoryUsed:      avgMemory,
		MemoryMax:       maxMemory,
		MemoryRecent:    recentMemory,
		MemoryPercent:   memoryPercent,
		InvocationCount: len(invocations),
		TimeoutCount:    0, // Can't determine from logs alone
		ErrorCount:      0, // Can't determine from logs alone
		UsedByStepFuncs: usedByStepFuncs,
		LastInvoked:     lastInvoked,
		FirstInvoked:    firstInvoked,
	}

	return metrics, nil
}

// GetTimeoutSeconds returns the timeout in seconds as an int for easier display
func (m *LambdaMetrics) GetTimeoutSeconds() int {
	return int(m.Timeout)
}

// GetTimeoutMillis returns the timeout in milliseconds for comparison
func (m *LambdaMetrics) GetTimeoutMillis() int64 {
	return int64(m.Timeout) * 1000
}

// FormatAvgDuration returns formatted average duration (e.g., "2.5s" or "450ms")
func (m *LambdaMetrics) FormatAvgDuration() string {
	return FormatDuration(m.AvgDuration)
}

// FormatMaxDuration returns formatted max duration
func (m *LambdaMetrics) FormatMaxDuration() string {
	return FormatDuration(m.MaxDuration)
}

// FormatRecentDuration returns formatted recent duration
func (m *LambdaMetrics) FormatRecentDuration() string {
	return FormatDuration(m.RecentDuration)
}

// FormatTimeout returns formatted timeout (e.g., "30s" or "1m30s")
func (m *LambdaMetrics) FormatTimeout() string {
	seconds := int(m.Timeout)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	remainingSeconds := seconds % 60
	if remainingSeconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, remainingSeconds)
}

// parseMemoryFromLogMessage extracts memory usage from Lambda REPORT log line
// Example: "REPORT RequestId: abc-123 Duration: 1234.56 ms ... Max Memory Used: 128 MB"
// DEPRECATED: Now using CloudWatch Logs Insights queries instead
func parseMemoryFromLogMessage(message string) int32 {
	// Look for "Max Memory Used: XXX MB"
	idx := strings.Index(message, "Max Memory Used:")
	if idx == -1 {
		return 0
	}

	// Extract the number after "Max Memory Used:"
	substr := message[idx+len("Max Memory Used:"):]
	// Find the MB marker
	mbIdx := strings.Index(substr, "MB")
	if mbIdx == -1 {
		return 0
	}

	// Extract number between "Max Memory Used:" and "MB"
	numStr := strings.TrimSpace(substr[:mbIdx])

	// Parse the integer
	var memMB int32
	_, err := fmt.Sscanf(numStr, "%d", &memMB)
	if err != nil {
		return 0
	}

	return memMB
}

// ExtractLambdaArnFromResource extracts Lambda function name from various ARN formats
// Handles both Lambda ARNs and state machine resource references
func ExtractLambdaArnFromResource(resource string) (string, bool) {
	if resource == "" {
		return "", false
	}

	// Lambda ARN format: arn:aws:lambda:region:account-id:function:function-name
	if strings.Contains(resource, ":lambda:") && strings.Contains(resource, ":function:") {
		parts := strings.Split(resource, ":")
		if len(parts) >= 7 {
			// Function name is after ":function:"
			functionName := strings.Join(parts[6:], ":")
			return functionName, true
		}
	}

	// Direct function name (sometimes used in resource field)
	if !strings.Contains(resource, ":") && resource != "" {
		return resource, true
	}

	return "", false
}

// GetFunctionNameFromArn extracts the function name from a Lambda ARN
func GetFunctionNameFromArn(arn string) string {
	if arn == "" {
		return ""
	}

	// ARN format: arn:aws:lambda:region:account-id:function:function-name
	parts := strings.Split(arn, ":")
	if len(parts) >= 7 && parts[5] == "function" {
		return strings.Join(parts[6:], ":")
	}

	// If not an ARN, return as-is (might already be a function name)
	if !strings.Contains(arn, ":") {
		return arn
	}

	return ""
}

// FormatDuration formats milliseconds into human-readable duration
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

// InvocationData represents a single Lambda invocation from CloudWatch Logs
type InvocationData struct {
	Timestamp  time.Time
	Duration   int64 // milliseconds
	MemoryUsed int32 // MB
	MemorySize int32 // MB (allocated)
}

// GetLast20Invocations queries CloudWatch Logs Insights for the last 20 Lambda invocations
func (c *Client) GetLast20Invocations(ctx context.Context, functionName string) ([]InvocationData, error) {
	logGroupName := fmt.Sprintf("/aws/lambda/%s", functionName)

	// CloudWatch Logs Insights query for last 20 REPORT lines
	query := `fields @timestamp, @duration, @maxMemoryUsed, @memorySize
| filter @type = "REPORT"
| sort @timestamp desc
| limit 20`

	// Query last 24 hours
	startTime := aws.Int64(time.Now().Add(-24 * time.Hour).Unix())
	endTime := aws.Int64(time.Now().Unix())

	// Start query
	startResp, err := c.Logs.StartQuery(ctx, &cloudwatchlogs.StartQueryInput{
		LogGroupName: aws.String(logGroupName),
		QueryString:  aws.String(query),
		StartTime:    startTime,
		EndTime:      endTime,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start query: %w", err)
	}

	// Poll for results (with timeout)
	queryId := startResp.QueryId
	maxAttempts := 30 // 15 seconds max (30 * 500ms)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		time.Sleep(500 * time.Millisecond)

		results, err := c.Logs.GetQueryResults(ctx, &cloudwatchlogs.GetQueryResultsInput{
			QueryId: queryId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get query results: %w", err)
		}

		switch results.Status {
		case "Complete":
			return parseInvocationResults(results.Results), nil
		case "Failed", "Cancelled", "Timeout":
			return nil, fmt.Errorf("query %s", results.Status)
		case "Running", "Scheduled":
			// Continue polling
			continue
		}
	}

	return nil, fmt.Errorf("query timed out after %d attempts", maxAttempts)
}

// parseInvocationResults parses CloudWatch Logs Insights results into InvocationData
func parseInvocationResults(results [][]cwltypes.ResultField) []InvocationData {
	invocations := make([]InvocationData, 0, len(results))

	for _, result := range results {
		inv := InvocationData{}
		hasData := false

		for _, field := range result {
			if field.Field == nil || field.Value == nil {
				continue
			}

			fieldName := *field.Field
			fieldValue := *field.Value

			switch fieldName {
			case "@timestamp":
				// Parse timestamp: "2024-01-15 10:30:45.123"
				if t, err := time.Parse("2006-01-02 15:04:05.000", fieldValue); err == nil {
					inv.Timestamp = t
					hasData = true
				}
			case "@duration":
				// Duration in milliseconds
				var duration float64
				if _, err := fmt.Sscanf(fieldValue, "%f", &duration); err == nil {
					inv.Duration = int64(duration)
					hasData = true
				}
			case "@maxMemoryUsed":
				// Memory used in bytes (CloudWatch returns bytes, convert to MB)
				var memUsedBytes int64
				if _, err := fmt.Sscanf(fieldValue, "%d", &memUsedBytes); err == nil {
					inv.MemoryUsed = int32(memUsedBytes / (1024 * 1024)) // Convert bytes to MB
					hasData = true
				}
			case "@memorySize":
				// Allocated memory in bytes (CloudWatch returns bytes, convert to MB)
				var memSizeBytes int64
				if _, err := fmt.Sscanf(fieldValue, "%d", &memSizeBytes); err == nil {
					inv.MemorySize = int32(memSizeBytes / (1024 * 1024)) // Convert bytes to MB
					hasData = true
				}
			}
		}

		if hasData {
			invocations = append(invocations, inv)
		}
	}

	return invocations
}
