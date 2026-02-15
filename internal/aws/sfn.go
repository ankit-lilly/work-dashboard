package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

const (
	execCacheTTLMin = 10 * time.Second
	execCacheTTLMax = 30 * time.Second
	execCacheMax    = 512
)

type execCacheKey struct {
	arn       string
	status    types.ExecutionStatus
	max       int32
	nextToken string
}

type execCacheEntry struct {
	out       *sfn.ListExecutionsOutput
	expiresAt time.Time
}

type Execution struct {
	Env               string
	Name              string
	ExecutionName     string
	ExecutionArn      string
	Status            string
	StartTime         time.Time
	StopTime          time.Time
	Duration          string
	ErrorType         string
	FailureReason     string
	FailedAt          string
	Stale             bool
	New               bool
	InputBucket       string
	InputKey          string
	InputSize         string
	OutputBucket      string
	OutputPrefix      string
	OutputManifestKey string
}

func (c *Client) ListActiveExecutions(ctx context.Context) ([]Execution, error) {
	stateMachines, err := c.getStateMachines(ctx, 5*time.Minute)
	if err != nil {
		return nil, err
	}

	var active []Execution
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, sm := range stateMachines {
		wg.Add(1)
		sem <- struct{}{}
		go func(sm types.StateMachineListItem) {
			defer wg.Done()
			defer func() { <-sem }()

			execs, err := c.listExecutionsCached(ctx, &sfn.ListExecutionsInput{
				StateMachineArn: sm.StateMachineArn,
				StatusFilter:    types.ExecutionStatusRunning,
				MaxResults:      10,
			})
			if err != nil {
				slog.Warn("list executions failed", "env", c.EnvName, "state_machine_arn", derefString(sm.StateMachineArn), "err", err)
				return
			}

			local := make([]Execution, 0, len(execs.Executions))
			for _, e := range execs.Executions {
				if e.StartDate == nil {
					continue
				}
				duration := time.Since(*e.StartDate).Round(time.Second).String()
				local = append(local, Execution{
					Env:           c.EnvName,
					Name:          *sm.Name,
					ExecutionName: *e.Name,
					ExecutionArn:  *e.ExecutionArn,
					Status:        string(e.Status),
					StartTime:     *e.StartDate,
					Duration:      duration,
				})
			}

			if len(local) == 0 {
				return
			}
			mu.Lock()
			active = append(active, local...)
			mu.Unlock()
		}(sm)
	}
	wg.Wait()

	return active, nil
}

func (c *Client) ListRecentFailures(ctx context.Context) ([]Execution, error) {
	stateMachines, err := c.getStateMachines(ctx, 10*time.Minute)
	if err != nil {
		return nil, err
	}

	var failures []Execution
	oneDayAgo := time.Now().Add(-24 * time.Hour)

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, sm := range stateMachines {
		wg.Add(1)
		sem <- struct{}{}
		go func(sm types.StateMachineListItem) {
			defer wg.Done()
			defer func() { <-sem }()

			execs, err := c.listExecutionsCached(ctx, &sfn.ListExecutionsInput{
				StateMachineArn: sm.StateMachineArn,
				StatusFilter:    types.ExecutionStatusFailed,
				MaxResults:      10,
			})
			if err != nil {
				slog.Warn("list executions failed", "env", c.EnvName, "state_machine_arn", derefString(sm.StateMachineArn), "err", err)
				return
			}

			local := make([]Execution, 0, len(execs.Executions))
			for _, e := range execs.Executions {
				if e.StopDate == nil || e.StopDate.Before(oneDayAgo) {
					continue
				}
				var errorType string
				var failureReason string
				desc, err := c.Sfn.DescribeExecution(ctx, &sfn.DescribeExecutionInput{
					ExecutionArn: e.ExecutionArn,
				})
				if err != nil {
					slog.Warn("describe execution failed", "env", c.EnvName, "execution_arn", derefString(e.ExecutionArn), "err", err)
				} else {
					if desc.Error != nil {
						errorType = *desc.Error
					}
					if desc.Cause != nil {
						failureReason = *desc.Cause
					}
				}

				local = append(local, Execution{
					Env:           c.EnvName,
					Name:          *sm.Name,
					ExecutionName: *e.Name,
					ExecutionArn:  *e.ExecutionArn,
					Status:        string(e.Status),
					StopTime:      *e.StopDate,
					FailedAt:      e.StopDate.In(time.Local).Format("15:04:05 MST (02 Jan)"),
					ErrorType:     errorTypeOrDefault(errorType),
					FailureReason: failureReason,
				})
			}

			if len(local) == 0 {
				return
			}
			mu.Lock()
			failures = append(failures, local...)
			mu.Unlock()
		}(sm)
	}
	wg.Wait()

	return failures, nil
}

func (c *Client) ListRecentCompleted(ctx context.Context, since time.Time) ([]Execution, error) {
	stateMachines, err := c.getStateMachines(ctx, 10*time.Minute)
	if err != nil {
		return nil, err
	}

	var completed []Execution
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for _, sm := range stateMachines {
		wg.Add(1)
		sem <- struct{}{}
		go func(sm types.StateMachineListItem) {
			defer wg.Done()
			defer func() { <-sem }()

			execs, err := c.listExecutionsCached(ctx, &sfn.ListExecutionsInput{
				StateMachineArn: sm.StateMachineArn,
				StatusFilter:    types.ExecutionStatusSucceeded,
				MaxResults:      10,
			})
			if err != nil {
				slog.Warn("list executions failed", "env", c.EnvName, "state_machine_arn", derefString(sm.StateMachineArn), "err", err)
				return
			}

			local := make([]Execution, 0, len(execs.Executions))
			for _, e := range execs.Executions {
				if e.StopDate == nil || e.StopDate.Before(since) {
					continue
				}
				duration := ""
				if e.StartDate != nil && e.StopDate != nil {
					d := e.StopDate.Sub(*e.StartDate)
					if d < 0 {
						d = -d
					}
					duration = d.Round(time.Second).String()
				}
				local = append(local, Execution{
					Env:           c.EnvName,
					Name:          derefString(sm.Name),
					ExecutionName: derefString(e.Name),
					ExecutionArn:  derefString(e.ExecutionArn),
					Status:        string(e.Status),
					StartTime:     derefTime(e.StartDate),
					StopTime:      derefTime(e.StopDate),
					Duration:      duration,
				})
			}

			if len(local) == 0 {
				return
			}
			mu.Lock()
			completed = append(completed, local...)
			mu.Unlock()
		}(sm)
	}
	wg.Wait()

	return completed, nil
}

func (c *Client) getStateMachines(ctx context.Context, ttl time.Duration) ([]types.StateMachineListItem, error) {
	c.StateMachinesMu.RLock()
	if len(c.StateMachines) > 0 && time.Since(c.StateMachinesAt) < ttl {
		cached := make([]types.StateMachineListItem, len(c.StateMachines))
		copy(cached, c.StateMachines)
		c.StateMachinesMu.RUnlock()
		return cached, nil
	}
	c.StateMachinesMu.RUnlock()

	var all []types.StateMachineListItem
	var nextToken *string
	for {
		out, err := c.Sfn.ListStateMachines(ctx, &sfn.ListStateMachinesInput{
			MaxResults: 100,
			NextToken:  nextToken,
		})
		if err != nil {
			return nil, err
		}
		for _, sm := range out.StateMachines {
			if includeStateMachineName(sm.Name) {
				all = append(all, sm)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	c.StateMachinesMu.Lock()
	c.StateMachines = all
	c.StateMachinesAt = time.Now()
	c.StateMachinesMu.Unlock()

	return all, nil
}

func (c *Client) ListFilteredStateMachines(ctx context.Context, ttl time.Duration) ([]types.StateMachineListItem, error) {
	return c.getStateMachines(ctx, ttl)
}

func (c *Client) ListRecentExecutionsByStatus(ctx context.Context, stateMachineArn *string, status types.ExecutionStatus, cutoff time.Time, maxPages int) ([]types.ExecutionListItem, error) {
	var out []types.ExecutionListItem
	var nextToken *string

	for range maxPages {
		resp, err := c.listExecutionsCached(ctx, &sfn.ListExecutionsInput{
			StateMachineArn: stateMachineArn,
			StatusFilter:    status,
			MaxResults:      100,
			NextToken:       nextToken,
		})
		if err != nil {
			return nil, err
		}

		for _, e := range resp.Executions {
			if e.StartDate != nil && e.StartDate.Before(cutoff) {
				return out, nil
			}
			out = append(out, e)
		}

		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}

	return out, nil
}

func (c *Client) listExecutionsCached(ctx context.Context, input *sfn.ListExecutionsInput) (*sfn.ListExecutionsOutput, error) {
	key := execCacheKey{
		arn:       derefString(input.StateMachineArn),
		status:    input.StatusFilter,
		max:       input.MaxResults,
		nextToken: derefString(input.NextToken),
	}
	now := time.Now()

	c.execCacheMu.Lock()
	if entry, ok := c.execCache[key]; ok && now.Before(entry.expiresAt) {
		out := entry.out
		c.execCacheMu.Unlock()
		return out, nil
	}
	c.execCacheMu.Unlock()

	if c.execLimiter != nil {
		if err := c.execLimiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	out, err := c.listExecutionsWithRetry(ctx, input)
	if err != nil {
		return nil, err
	}

	ttl := randomTTL(execCacheTTLMin, execCacheTTLMax)
	exp := now.Add(ttl)

	c.execCacheMu.Lock()
	c.execCache[key] = execCacheEntry{
		out:       out,
		expiresAt: exp,
	}
	if len(c.execCache) > execCacheMax {
		for k, v := range c.execCache {
			if now.After(v.expiresAt) {
				delete(c.execCache, k)
			}
		}
	}
	c.execCacheMu.Unlock()

	return out, nil
}

func randomTTL(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	delta := max - min
	return min + time.Duration(rand.Int63n(int64(delta)))
}

func includeStateMachineName(name *string) bool {
	if name == nil {
		return false
	}
	n := *name

	if !strings.HasPrefix(n, "mdids-account-automation-relv305-backend") &&
		!strings.HasPrefix(n, "mdids-account-automation-backend") &&
		!strings.HasPrefix(n, "atom5-qa-stack-") &&
		!strings.HasPrefix(n, "lrl-dtf-sdr") {
		return false
	}

	// Allow all lrl-dtf-sdr prefixed state machines without suffix filtering.
	if strings.HasPrefix(n, "lrl-dtf-sdr") {
		return true
	}

	suffixes := []string{
		"BusRulesProcessor",
		"ATOM5RulesProcessor",
		"VeevaRulesProcessor",
		"IWRSRulesProcessor",
		"IWRSDataProcessor",
		"VeevaDataProcessor",
		"Atom5DataProcessor",
		"Infohub",
	}

	for _, suf := range suffixes {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	return false
}

func (c *Client) listExecutionsWithRetry(ctx context.Context, input *sfn.ListExecutionsInput) (*sfn.ListExecutionsOutput, error) {
	var lastErr error
	backoff := 400 * time.Millisecond

	for range [8]struct{}{} {
		out, err := c.Sfn.ListExecutions(ctx, input)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !isThrottlingErr(err) {
			return nil, err
		}

		sleep := withJitter(backoff)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}

		backoff *= 2
		if backoff > 6*time.Second {
			backoff = 6 * time.Second
		}
	}

	return nil, lastErr
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func isThrottlingErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Throttling") ||
		strings.Contains(msg, "Rate exceeded") ||
		strings.Contains(msg, "retry quota exceeded") ||
		strings.Contains(msg, "failed to get rate limit token")
}

func withJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	// Simple deterministic jitter based on current time; avoids extra rng imports.
	nanos := time.Now().UnixNano()
	jitter := time.Duration(nanos % int64(d/2+1)) // up to ~50% jitter
	return d + jitter
}

func errorTypeOrDefault(errorType string) string {
	if strings.TrimSpace(errorType) == "" {
		return "Execution Failed"
	}
	return errorType
}

// lambdaInvocation tracks a Lambda invocation during execution history parsing
type lambdaInvocation struct {
	functionArn string
	stateName   string
	startTime   *time.Time
	endTime     *time.Time
	timedOut    bool
	failed      bool
}

// ExtractLambdaResourcesFromExecution extracts Lambda function ARNs and invocation details from execution history
func (c *Client) ExtractLambdaResourcesFromExecution(ctx context.Context, executionArn string, stateMachineName string) (map[string]*LambdaResource, error) {
	history, err := c.Sfn.GetExecutionHistory(ctx, &sfn.GetExecutionHistoryInput{
		ExecutionArn: aws.String(executionArn),
		MaxResults:   1000,
		ReverseOrder: false,
	})
	if err != nil {
		return nil, err
	}

	resources := make(map[string]*LambdaResource)

	// Map event IDs to events for tracking state relationships
	evByID := make(map[int64]types.HistoryEvent)
	for _, ev := range history.Events {
		evByID[ev.Id] = ev
	}

	// Track Lambda invocations
	invocations := make(map[int64]*lambdaInvocation) // event ID -> invocation

	for _, event := range history.Events {
		switch event.Type {
		case types.HistoryEventTypeLambdaFunctionScheduled:
			if d := event.LambdaFunctionScheduledEventDetails; d != nil && d.Resource != nil {
				functionName, ok := ExtractLambdaArnFromResource(*d.Resource)
				if ok {
					invocations[event.Id] = &lambdaInvocation{
						functionArn: *d.Resource,
						stateName:   getStateNameFromEvent(evByID, event),
						startTime:   event.Timestamp,
					}

					if _, exists := resources[functionName]; !exists {
						resources[functionName] = &LambdaResource{
							FunctionName: functionName,
							FunctionArn:  *d.Resource,
							Invocations:  []InvocationRecord{},
							LastSeen:     time.Now(),
						}
					}
				}
			}

		case types.HistoryEventTypeLambdaFunctionSucceeded:
			if d := event.LambdaFunctionSucceededEventDetails; d != nil {
				// Find the corresponding scheduled event
				inv := findLambdaInvocationByPreviousEvent(invocations, evByID, event.PreviousEventId)
				if inv != nil {
					inv.endTime = event.Timestamp
				}
			}

		case types.HistoryEventTypeLambdaFunctionFailed:
			if d := event.LambdaFunctionFailedEventDetails; d != nil {
				inv := findLambdaInvocationByPreviousEvent(invocations, evByID, event.PreviousEventId)
				if inv != nil {
					inv.endTime = event.Timestamp
					inv.failed = true
				}
			}

		case types.HistoryEventTypeLambdaFunctionTimedOut:
			if d := event.LambdaFunctionTimedOutEventDetails; d != nil {
				inv := findLambdaInvocationByPreviousEvent(invocations, evByID, event.PreviousEventId)
				if inv != nil {
					inv.endTime = event.Timestamp
					inv.timedOut = true
				}
			}
		}
	}

	// Convert invocations to records
	for _, inv := range invocations {
		if inv.startTime == nil {
			continue
		}

		functionName, ok := ExtractLambdaArnFromResource(inv.functionArn)
		if !ok {
			continue
		}

		duration := int64(0)
		if inv.endTime != nil {
			duration = inv.endTime.Sub(*inv.startTime).Milliseconds()
		}

		record := InvocationRecord{
			ExecutionArn: executionArn,
			StateName:    inv.stateName,
			Duration:     duration,
			TimedOut:     inv.timedOut,
			Failed:       inv.failed,
			Timestamp:    *inv.startTime,
		}

		if res, exists := resources[functionName]; exists {
			res.Invocations = append(res.Invocations, record)
			res.LastSeen = time.Now()
		}
	}

	return resources, nil
}

// Helper function to find Lambda invocation by walking back through previous events
func findLambdaInvocationByPreviousEvent(invocations map[int64]*lambdaInvocation, evByID map[int64]types.HistoryEvent, prevID int64) *lambdaInvocation {
	// Walk back through previous events to find the scheduled event
	for prevID != 0 {
		if inv, ok := invocations[prevID]; ok {
			return inv
		}
		if prevEvent, ok := evByID[prevID]; ok {
			prevID = prevEvent.PreviousEventId
		} else {
			break
		}
	}
	return nil
}

// Helper function to extract state name from event context
func getStateNameFromEvent(evByID map[int64]types.HistoryEvent, event types.HistoryEvent) string {
	// Walk back to find the state entered event
	prevID := event.PreviousEventId
	for prevID != 0 {
		if prevEvent, ok := evByID[prevID]; ok {
			if prevEvent.StateEnteredEventDetails != nil && prevEvent.StateEnteredEventDetails.Name != nil {
				return *prevEvent.StateEnteredEventDetails.Name
			}
			prevID = prevEvent.PreviousEventId
		} else {
			break
		}
	}
	return ""
}

// ExtractLambdaFunctionsFromStateMachine parses a Step Function state machine definition
// and extracts all Lambda function ARNs referenced in the workflow
func (c *Client) ExtractLambdaFunctionsFromStateMachine(ctx context.Context, stateMachineArn string) ([]string, error) {
	// Get state machine definition
	desc, err := c.Sfn.DescribeStateMachine(ctx, &sfn.DescribeStateMachineInput{
		StateMachineArn: &stateMachineArn,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe state machine: %w", err)
	}

	if desc.Definition == nil {
		return []string{}, nil
	}

	// Parse JSON definition
	var definition map[string]any
	if err := json.Unmarshal([]byte(*desc.Definition), &definition); err != nil {
		return nil, fmt.Errorf("failed to parse state machine definition: %w", err)
	}

	// Extract Lambda ARNs from states
	lambdaArns := make(map[string]bool) // Use map to deduplicate
	extractLambdaArnsFromStates(definition, lambdaArns)

	// Convert to slice
	result := make([]string, 0, len(lambdaArns))
	for arn := range lambdaArns {
		result = append(result, arn)
	}

	return result, nil
}

// extractLambdaArnsFromStates recursively walks state machine definition to find Lambda ARNs
func extractLambdaArnsFromStates(definition map[string]any, lambdaArns map[string]bool) {
	states, ok := definition["States"].(map[string]any)
	if !ok {
		return
	}

	for _, stateValue := range states {
		state, ok := stateValue.(map[string]any)
		if !ok {
			continue
		}

		stateType, _ := state["Type"].(string)

		switch stateType {
		case "Task":
			// Extract Lambda ARN from Task state
			if resource, ok := state["Resource"].(string); ok {
				if arn := extractLambdaArnFromTaskResource(resource, state); arn != "" {
					lambdaArns[arn] = true
				}
			}

		case "Parallel":
			// Recursively process branches
			if branches, ok := state["Branches"].([]any); ok {
				for _, branchValue := range branches {
					if branch, ok := branchValue.(map[string]any); ok {
						extractLambdaArnsFromStates(branch, lambdaArns)
					}
				}
			}

		case "Map":
			// Recursively process iterator
			if iterator, ok := state["Iterator"].(map[string]any); ok {
				extractLambdaArnsFromStates(iterator, lambdaArns)
			}
			// Also check ItemProcessor for distributed map (newer format)
			if itemProcessor, ok := state["ItemProcessor"].(map[string]any); ok {
				if processorConfig, ok := itemProcessor["ProcessorConfig"].(map[string]any); ok {
					extractLambdaArnsFromStates(processorConfig, lambdaArns)
				}
				// Check StartAt and States in ItemProcessor
				extractLambdaArnsFromStates(itemProcessor, lambdaArns)
			}
		}
	}
}

// extractLambdaArnFromTaskResource extracts Lambda ARN from various resource formats
func extractLambdaArnFromTaskResource(resource string, state map[string]any) string {
	// Format 1: Direct Lambda ARN
	// "arn:aws:lambda:region:account:function:FunctionName"
	// "arn:aws:lambda:region:account:function:FunctionName:$LATEST"
	if strings.Contains(resource, ":lambda:") && strings.Contains(resource, ":function:") {
		// Remove version/alias suffix if present
		if idx := strings.LastIndex(resource, ":"); idx != -1 {
			suffix := resource[idx+1:]
			// Check if it's a version ($LATEST) or alias
			if suffix == "$LATEST" || strings.HasPrefix(suffix, "$") {
				return resource[:idx]
			}
		}
		return resource
	}

	// Format 2: Lambda invoke API with Parameters.FunctionName
	// "Resource": "arn:aws:states:::lambda:invoke"
	if strings.Contains(resource, ":states:") && strings.Contains(resource, ":lambda:invoke") {
		// Look for FunctionName in Parameters
		if params, ok := state["Parameters"].(map[string]any); ok {
			if functionName, ok := params["FunctionName"].(string); ok {
				// FunctionName could be an ARN or just a name
				if strings.HasPrefix(functionName, "arn:") {
					return functionName
				}
				// If it's just a name, we can't construct full ARN without account/region
				// Return as-is and handle in caller
				return functionName
			}
			// Check for FunctionName.$ (dynamic reference)
			if functionNamePath, ok := params["FunctionName.$"].(string); ok {
				// This is a JSON path reference, can't statically determine
				// Skip these for now
				_ = functionNamePath
			}
		}
	}

	return ""
}
