package server

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/starfederation/datastar-go/datastar"
)

// LambdaRegistry tracks discovered Lambda functions from Step Function state machine definitions
type LambdaRegistry struct {
	mu              sync.RWMutex
	lambdaFunctions map[string]*LambdaFunction // key: env:functionName
	lastDiscovery   time.Time
}

// LambdaFunction represents a Lambda function discovered from Step Function definitions
type LambdaFunction struct {
	Env             string
	FunctionName    string
	FunctionArn     string
	UsedByStepFuncs []string // List of Step Function names
}

// LambdaReport contains all Lambda metrics for broadcasting
type LambdaReport struct {
	Metrics      []*aws.LambdaMetrics
	WarningCount int
	LastUpdated  time.Time
}

// cachedLambdaMetrics stores cached metrics with timestamp
type cachedLambdaMetrics struct {
	Metrics   *aws.LambdaMetrics
	FetchedAt time.Time
}

// discoverLambdaFunctions discovers Lambda functions from Step Function state machine definitions
// This runs periodically (every 15 minutes) since definitions don't change often
func (s *Server) discoverLambdaFunctions(ctx context.Context) error {
	now := time.Now()

	// Check if we need to run discovery (15 minute cache)
	s.resourceRegistryMu.Lock()
	if s.lambdaRegistry != nil && now.Sub(s.lambdaRegistry.lastDiscovery) < 15*time.Minute {
		s.resourceRegistryMu.Unlock()
		return nil // Skip - definitions haven't changed
	}
	s.resourceRegistryMu.Unlock()

	slog.Info("lambda discovery: starting discovery from state machine definitions")

	registry := &LambdaRegistry{
		lambdaFunctions: make(map[string]*LambdaFunction),
		lastDiscovery:   now,
	}

	discoveredCount := 0

	// For each AWS client (environment)
	for _, client := range s.awsManager.Clients {
		// List all state machines
		stateMachines, err := client.ListFilteredStateMachines(ctx, 1*time.Hour)
		if err != nil {
			if aws.IsCredentialError(err) {
				continue
			}
			slog.Warn("failed to list state machines for lambda discovery", "env", client.EnvName, "error", err)
			continue
		}

		slog.Debug("lambda discovery: processing state machines", "env", client.EnvName, "count", len(stateMachines))

		// Extract Lambda ARNs from each state machine definition
		for _, sm := range stateMachines {
			lambdaArns, err := client.ExtractLambdaFunctionsFromStateMachine(ctx, *sm.StateMachineArn)
			if err != nil {
				slog.Debug("failed to extract lambdas from state machine", "env", client.EnvName, "sm", *sm.Name, "error", err)
				continue
			}

			if len(lambdaArns) > 0 {
				slog.Debug("lambda discovery: extracted lambdas from state machine", "env", client.EnvName, "sm", *sm.Name, "lambda_count", len(lambdaArns))
				discoveredCount += len(lambdaArns)
			}

			// Add each Lambda to the registry
			for _, arn := range lambdaArns {
				funcName := aws.GetFunctionNameFromArn(arn)
				if funcName == "" {
					funcName = arn // Use as-is if can't extract name
				}

				key := fmt.Sprintf("%s:%s", client.EnvName, funcName)

				if existing, ok := registry.lambdaFunctions[key]; ok {
					// Add this Step Function to the list
					existing.UsedByStepFuncs = append(existing.UsedByStepFuncs, *sm.Name)
				} else {
					// Create new entry
					registry.lambdaFunctions[key] = &LambdaFunction{
						Env:             client.EnvName,
						FunctionName:    funcName,
						FunctionArn:     arn,
						UsedByStepFuncs: []string{*sm.Name},
					}
				}
			}
		}
	}

	totalLambdas := len(registry.lambdaFunctions)
	slog.Info("lambda discovery: completed", "total_lambdas", totalLambdas, "total_discovered", discoveredCount)

	// Update the registry
	s.resourceRegistryMu.Lock()
	s.lambdaRegistry = registry
	s.resourceRegistryMu.Unlock()

	return nil
}

// fetchLambdaMetrics fetches Lambda metrics with adaptive polling and per-function caching
func (s *Server) fetchLambdaMetrics() (*LambdaReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Discover Lambda functions from state machine definitions (every 15 min)
	if err := s.discoverLambdaFunctions(ctx); err != nil {
		slog.Warn("failed to discover lambda functions", "error", err)
	}

	// Determine polling interval based on active executions
	hasActiveExecs := s.getActiveExecutionsCount() > 0
	interval := 5 * time.Minute // Idle interval
	if hasActiveExecs {
		interval = 1 * time.Minute // Active interval
	}

	// Collect metrics for each discovered Lambda
	var allMetrics []*aws.LambdaMetrics
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Limit concurrent Lambda API calls

	s.resourceRegistryMu.RLock()
	if s.lambdaRegistry == nil || len(s.lambdaRegistry.lambdaFunctions) == 0 {
		s.resourceRegistryMu.RUnlock()
		// No Lambdas discovered yet
		return &LambdaReport{
			Metrics:      []*aws.LambdaMetrics{},
			WarningCount: 0,
			LastUpdated:  time.Now(),
		}, nil
	}

	totalLambdas := len(s.lambdaRegistry.lambdaFunctions)
	slog.Info("fetching lambda metrics", "total_lambdas", totalLambdas, "interval", interval)

	cachedCount := 0
	fetchCount := 0

	for key, lambdaFunc := range s.lambdaRegistry.lambdaFunctions {
		// Check cache first
		now := time.Now()
		s.lambdaCacheMu.Lock()
		if cached, ok := s.lambdaCache[key]; ok {
			if now.Sub(cached.FetchedAt) < interval {
				allMetrics = append(allMetrics, cached.Metrics)
				cachedCount++
				s.lambdaCacheMu.Unlock()
				continue
			}
		}
		s.lambdaCacheMu.Unlock()

		// Fetch fresh metrics
		fetchCount++
		wg.Add(1)
		sem <- struct{}{}
		go func(lf *LambdaFunction, cacheKey string) {
			defer wg.Done()
			defer func() { <-sem }()

			client := s.awsManager.Clients[lf.Env]
			if client == nil {
				return
			}

			// Fetch metrics using CloudWatch Logs Insights (last 20 invocations)
			metrics, err := client.GetLambdaMetrics(ctx, lf.FunctionName, lf.UsedByStepFuncs)
			if err != nil {
				slog.Debug("failed to get lambda metrics", "env", lf.Env, "function", lf.FunctionName, "error", err)
				return
			}

			// Cache the result
			s.lambdaCacheMu.Lock()
			s.lambdaCache[cacheKey] = cachedLambdaMetrics{
				Metrics:   metrics,
				FetchedAt: now,
			}
			s.lambdaCacheMu.Unlock()

			mu.Lock()
			allMetrics = append(allMetrics, metrics)
			mu.Unlock()
		}(lambdaFunc, key)
	}
	s.resourceRegistryMu.RUnlock()

	wg.Wait()

	slog.Info("lambda metrics fetch complete", "cached", cachedCount, "fetched", fetchCount, "total", len(allMetrics))

	// Sort by timeout percentage (highest first)
	sort.Slice(allMetrics, func(i, j int) bool {
		return allMetrics[i].TimeoutPercent > allMetrics[j].TimeoutPercent
	})

	// Count warnings (>= 90% timeout)
	warningCount := 0
	for _, m := range allMetrics {
		if m.TimeoutPercent >= aws.TimeoutWarningHigh {
			warningCount++
		}
	}

	return &LambdaReport{
		Metrics:      allMetrics,
		WarningCount: warningCount,
		LastUpdated:  time.Now(),
	}, nil
}

// handleLambdaUpdates handles SSE updates for Lambda metrics
func (s *Server) handleLambdaUpdates(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ctx := r.Context()

	lambdaCh := s.lambdaBroadcaster.Subscribe()
	defer s.lambdaBroadcaster.Unsubscribe(lambdaCh)

	// Send initial loading state
	sse.PatchSignals([]byte(`{"lambda_warnings": 0, "lambda_count": 0}`))

	// Immediately send any cached Lambda metrics
	s.sendCachedLambdaMetrics(sse)

	for {
		select {
		case <-ctx.Done():
			return
		case report, ok := <-lambdaCh:
			if !ok {
				return
			}

			// Check for credential errors
			hasCredError, credMsg, _ := s.getCredentialError()
			if hasCredError {
				// Update credential error signal
				sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, credMsg)))
				continue
			}

			// Update signals
			signals := fmt.Sprintf(`{"lambda_warnings": %d, "lambda_count": %d}`,
				report.WarningCount, len(report.Metrics))
			sse.PatchSignals([]byte(signals))

			// Render warnings section
			var warningsBuf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				continue
			}

			warnings := make([]*aws.LambdaMetrics, 0)
			for _, m := range report.Metrics {
				if m.TimeoutPercent >= aws.TimeoutWarningHigh {
					warnings = append(warnings, m)
				}
			}

			if err := tmpl.ExecuteTemplate(&warningsBuf, "lambda-warnings", map[string]any{
				"Warnings": warnings,
			}); err != nil {
				slog.Error("template render failed", "template", "lambda-warnings", "error", err)
				continue
			}

			sse.PatchElements(warningsBuf.String(),
				datastar.WithSelector("#lambda-warnings-content"),
				datastar.WithMode(datastar.ElementPatchModeInner),
			)

			// Render all resources
			var resourcesBuf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&resourcesBuf, "lambda-resources", map[string]any{
				"Metrics": report.Metrics,
			}); err != nil {
				slog.Error("template render failed", "template", "lambda-resources", "error", err)
				continue
			}

			sse.PatchElements(resourcesBuf.String(),
				datastar.WithSelector("#lambda-resources-content"),
				datastar.WithMode(datastar.ElementPatchModeInner),
			)
		}
	}
}

// getBaseEnv extracts base environment name (dev/qa/prod) from full env name
func getBaseEnv(env string) string {
	lower := strings.ToLower(env)
	if strings.Contains(lower, "prod") {
		return "prod"
	}
	if strings.Contains(lower, "qa") {
		return "qa"
	}
	if strings.Contains(lower, "dev") {
		return "dev"
	}
	return env
}
