package aws

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
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
	MemoryPercent   float64 // percentage of allocated memory used (based on average)
	InvocationCount int
	TimeoutCount    int
	ErrorCount      int
	UsedByStepFuncs []string // Which Step Functions use this Lambda
	LastInvoked     time.Time
	ConfigError     string // Error fetching configuration
}

// GetLambdaMetrics fetches Lambda configuration and calculates timeout metrics
// Uses hybrid approach: execution history for duration, CloudWatch only for >80% timeout warnings
func (c *Client) GetLambdaMetrics(ctx context.Context, functionName string, invocations []InvocationRecord, usedByStepFuncs []string) (*LambdaMetrics, error) {
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
			InvocationCount: len(invocations),
			UsedByStepFuncs: usedByStepFuncs,
		}, nil
	}

	timeout := aws.ToInt32(config.Timeout)
	memoryAllocated := aws.ToInt32(config.MemorySize)
	functionArn := aws.ToString(config.FunctionArn)

	// Calculate metrics from invocation records
	var totalDuration int64
	var maxDuration int64
	var recentDuration int64
	var timeoutCount int
	var errorCount int
	var lastInvoked time.Time

	for i, inv := range invocations {
		totalDuration += inv.Duration
		if inv.Duration > maxDuration {
			maxDuration = inv.Duration
		}
		if i == len(invocations)-1 {
			recentDuration = inv.Duration
			lastInvoked = inv.Timestamp
		}
		if inv.TimedOut {
			timeoutCount++
		}
		if inv.Failed {
			errorCount++
		}
	}

	avgDuration := int64(0)
	if len(invocations) > 0 {
		avgDuration = totalDuration / int64(len(invocations))
	}

	// Calculate timeout percentage based on average duration
	timeoutMillis := int64(timeout) * 1000
	timeoutPercent := 0.0
	if timeoutMillis > 0 {
		timeoutPercent = (float64(avgDuration) / float64(timeoutMillis)) * 100
	}

	// Fetch memory usage from CloudWatch Logs for recent invocations
	memoryUsed, memoryMax := int32(0), int32(0)
	// Skip memory fetch if no invocations yet
	if !lastInvoked.IsZero() {
		memoryUsed, memoryMax = c.getMemoryUsageFromLogs(ctx, functionName, lastInvoked)
	}

	// Calculate memory percentage
	memoryPercent := 0.0
	if memoryAllocated > 0 && memoryUsed > 0 {
		memoryPercent = (float64(memoryUsed) / float64(memoryAllocated)) * 100
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
		MemoryUsed:      memoryUsed,
		MemoryMax:       memoryMax,
		MemoryPercent:   memoryPercent,
		InvocationCount: len(invocations),
		TimeoutCount:    timeoutCount,
		ErrorCount:      errorCount,
		UsedByStepFuncs: usedByStepFuncs,
		LastInvoked:     lastInvoked,
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

// getMemoryUsageFromLogs fetches memory usage from CloudWatch Logs
// Lambda automatically logs: "REPORT RequestId: xxx ... Memory Size: 512 MB Max Memory Used: 128 MB"
func (c *Client) getMemoryUsageFromLogs(ctx context.Context, functionName string, since time.Time) (avgMemory int32, maxMemory int32) {
	// Construct log group name (Lambda standard format)
	logGroupName := fmt.Sprintf("/aws/lambda/%s", functionName)

	// Query last 1 hour or since last invocation
	startTime := since.Add(-1 * time.Hour).UnixMilli()
	if since.IsZero() {
		startTime = time.Now().Add(-1 * time.Hour).UnixMilli()
	}
	endTime := time.Now().UnixMilli()

	// Filter for REPORT lines that contain memory usage
	filterPattern := "[report_type=REPORT, ...]"

	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  aws.String(logGroupName),
		StartTime:     aws.Int64(startTime),
		EndTime:       aws.Int64(endTime),
		FilterPattern: aws.String(filterPattern),
		Limit:         aws.Int32(50), // Get last 50 invocations for averaging
	}

	result, err := c.Logs.FilterLogEvents(ctx, input)
	if err != nil {
		// Log error but don't fail - memory usage is optional
		slog.Debug("failed to fetch memory usage from logs", "env", c.EnvName, "function", functionName, "error", err)
		return 0, 0
	}

	if len(result.Events) == 0 {
		return 0, 0
	}

	// Parse memory usage from log messages
	var totalMemory int64
	var maxMem int32
	count := 0

	for _, event := range result.Events {
		if event.Message == nil {
			continue
		}

		// Parse: "Max Memory Used: 128 MB"
		message := *event.Message
		memUsed := parseMemoryFromLogMessage(message)
		if memUsed > 0 {
			totalMemory += int64(memUsed)
			if memUsed > maxMem {
				maxMem = memUsed
			}
			count++
		}
	}

	if count == 0 {
		return 0, 0
	}

	avgMem := int32(totalMemory / int64(count))
	return avgMem, maxMem
}

// parseMemoryFromLogMessage extracts memory usage from Lambda REPORT log line
// Example: "REPORT RequestId: abc-123 Duration: 1234.56 ms ... Max Memory Used: 128 MB"
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
