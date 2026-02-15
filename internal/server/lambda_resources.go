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

// ResourceRegistry tracks discovered Lambda functions from Step Function executions
type ResourceRegistry struct {
	mu              sync.RWMutex
	lambdaFunctions map[string]*DiscoveredLambda // key: env:functionName
	lastUpdated     time.Time
}

// DiscoveredLambda represents a Lambda function discovered from Step Function execution history
type DiscoveredLambda struct {
	Env             string
	FunctionName    string
	FunctionArn     string
	Invocations     []aws.InvocationRecord
	UsedByStepFuncs map[string]bool // Set of Step Function names
	LastSeen        time.Time
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

// updateResourceRegistry discovers Lambda functions from recent Step Function executions
func (s *Server) updateResourceRegistry(ctx context.Context) error {
	s.resourceRegistryMu.Lock()
	defer s.resourceRegistryMu.Unlock()

	if s.resourceRegistry == nil {
		s.resourceRegistry = &ResourceRegistry{
			lambdaFunctions: make(map[string]*DiscoveredLambda),
		}
	}

	// Collect all recent executions (active + failures + completed)
	var allExecutions []aws.Execution

	// Get active executions from cache
	s.activeCacheMu.Lock()
	for _, execs := range s.activeCache {
		allExecutions = append(allExecutions, execs...)
	}
	s.activeCacheMu.Unlock()

	// Get recent failures (last 24h)
	for _, client := range s.awsManager.Clients {
		failures, err := client.ListRecentFailures(ctx)
		if err != nil {
			if aws.IsCredentialError(err) {
				continue
			}
			slog.Warn("failed to list failures for lambda discovery", "env", client.EnvName, "error", err)
			continue
		}
		allExecutions = append(allExecutions, failures...)
	}

	// Get recent completed executions (last 1 hour)
	cutoff := time.Now().Add(-1 * time.Hour)
	for _, client := range s.awsManager.Clients {
		completed, err := client.ListRecentCompleted(ctx, cutoff)
		if err != nil {
			if aws.IsCredentialError(err) {
				continue
			}
			slog.Warn("failed to list completed for lambda discovery", "env", client.EnvName, "error", err)
			continue
		}
		allExecutions = append(allExecutions, completed...)
	}

	// Extract Lambda resources from execution histories
	for _, exec := range allExecutions {
		client := s.awsManager.Clients[exec.Env]
		if client == nil {
			continue
		}

		resources, err := client.ExtractLambdaResourcesFromExecution(ctx, exec.ExecutionArn, exec.Name)
		if err != nil {
			slog.Debug("failed to extract lambda resources", "env", exec.Env, "execution", exec.ExecutionArn, "error", err)
			continue
		}

		// Merge resources into registry
		for funcName, resource := range resources {
			key := fmt.Sprintf("%s:%s", exec.Env, funcName)

			existing, ok := s.resourceRegistry.lambdaFunctions[key]
			if !ok {
				// New Lambda discovered
				existing = &DiscoveredLambda{
					Env:             exec.Env,
					FunctionName:    funcName,
					FunctionArn:     resource.FunctionArn,
					Invocations:     []aws.InvocationRecord{},
					UsedByStepFuncs: make(map[string]bool),
					LastSeen:        time.Now(),
				}
				s.resourceRegistry.lambdaFunctions[key] = existing
			}

			// Update invocations (keep last 100 per function)
			existing.Invocations = append(existing.Invocations, resource.Invocations...)
			if len(existing.Invocations) > 100 {
				// Keep most recent 100
				sort.Slice(existing.Invocations, func(i, j int) bool {
					return existing.Invocations[i].Timestamp.After(existing.Invocations[j].Timestamp)
				})
				existing.Invocations = existing.Invocations[:100]
			}

			// Track which Step Function uses this Lambda
			existing.UsedByStepFuncs[exec.Name] = true
			existing.LastSeen = time.Now()
		}
	}

	s.resourceRegistry.lastUpdated = time.Now()
	return nil
}

// fetchLambdaMetrics fetches Lambda metrics with adaptive polling and per-function caching
func (s *Server) fetchLambdaMetrics() (*LambdaReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Determine polling interval based on active executions
	hasActiveExecs := s.getActiveExecutionsCount() > 0
	interval := 5 * time.Minute // Idle interval
	if hasActiveExecs {
		interval = 1 * time.Minute // Active interval
	}

	// Update resource registry from recent executions
	if err := s.updateResourceRegistry(ctx); err != nil {
		slog.Warn("failed to update resource registry", "error", err)
	}

	// Collect metrics for each discovered Lambda
	var allMetrics []*aws.LambdaMetrics
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // Limit concurrent Lambda API calls

	s.resourceRegistryMu.RLock()
	if s.resourceRegistry == nil || len(s.resourceRegistry.lambdaFunctions) == 0 {
		s.resourceRegistryMu.RUnlock()
		// No Lambdas discovered yet
		return &LambdaReport{
			Metrics:      []*aws.LambdaMetrics{},
			WarningCount: 0,
			LastUpdated:  time.Now(),
		}, nil
	}

	for key, lambda := range s.resourceRegistry.lambdaFunctions {
		// Check cache first
		now := time.Now()
		s.lambdaCacheMu.Lock()
		if cached, ok := s.lambdaCache[key]; ok {
			if now.Sub(cached.FetchedAt) < interval {
				allMetrics = append(allMetrics, cached.Metrics)
				s.lambdaCacheMu.Unlock()
				continue
			}
		}
		s.lambdaCacheMu.Unlock()

		// Fetch fresh metrics
		wg.Add(1)
		sem <- struct{}{}
		go func(lam *DiscoveredLambda, cacheKey string) {
			defer wg.Done()
			defer func() { <-sem }()

			client := s.awsManager.Clients[lam.Env]
			if client == nil {
				return
			}

			// Convert UsedByStepFuncs map to slice
			usedBy := make([]string, 0, len(lam.UsedByStepFuncs))
			for sfn := range lam.UsedByStepFuncs {
				usedBy = append(usedBy, sfn)
			}
			sort.Strings(usedBy)

			metrics, err := client.GetLambdaMetrics(ctx, lam.FunctionName, lam.Invocations, usedBy)
			if err != nil {
				slog.Debug("failed to get lambda metrics", "env", lam.Env, "function", lam.FunctionName, "error", err)
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
		}(lambda, key)
	}
	s.resourceRegistryMu.RUnlock()

	wg.Wait()

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
