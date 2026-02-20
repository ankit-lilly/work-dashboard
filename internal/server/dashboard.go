package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/starfederation/datastar-go/datastar"
)

type StateMachineItem struct {
	Env     string
	BaseEnv string
	Name    string
	Arn     string
}

func (s *Server) handleDashboardUpdates(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ctx := r.Context()

	// Subscribe to all broadcasters
	activeCh := s.activeBroadcaster.Subscribe()
	defer s.activeBroadcaster.Unsubscribe(activeCh)

	completedCh := s.completedBroadcaster.Subscribe()
	defer s.completedBroadcaster.Unsubscribe(completedCh)

	failuresCh := s.failuresBroadcaster.Subscribe()
	defer s.failuresBroadcaster.Unsubscribe(failuresCh)

	rdsCh := s.rdsBroadcaster.Subscribe()
	defer s.rdsBroadcaster.Unsubscribe(rdsCh)

	lambdaCh := s.lambdaBroadcaster.Subscribe()
	defer s.lambdaBroadcaster.Unsubscribe(lambdaCh)

	// Send initial credential error state if present
	if hasError, errMsg, _ := s.getCredentialError(); hasError {
		sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, errMsg)))
	}

	// Send initial signals for all sections
	sse.PatchSignals([]byte(`{"rds_loading": true, "rds_db_count": 0, "lambda_warnings": 0, "lambda_count": 0}`))

	// Send any cached data immediately
	s.sendCachedRDSMetrics(sse)
	s.sendCachedLambdaMetrics(sse)

	// Note: State machines are loaded on initial page render, no SSE update needed
	// Update different sections via SSE as data comes in.

	for {
		select {
		case <-ctx.Done():
			return
		case allActive, ok := <-activeCh:
			if !ok {
				return
			}

			// Check and send credential error state
			if hasError, errMsg, _ := s.getCredentialError(); hasError {
				sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, errMsg)))
			} else {
				sse.PatchSignals([]byte(`{"credential_error": false, "credential_error_msg": ""}`))
			}

			// Patch signals for counts
			sse.PatchSignals([]byte(fmt.Sprintf(`{"active_jobs_count": %d}`, len(allActive))))

			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}
			var joke string
			if len(allActive) == 0 {
				joke = s.getFunJoke(ctx)
			}
			err := tmpl.ExecuteTemplate(&buf, "active-jobs", map[string]any{
				"Jobs": allActive,
				"Joke": joke,
			})
			if err != nil {
				return
			}

			sse.PatchElements(
				buf.String(),
				datastar.WithSelector("#active-jobs-list"),
				datastar.WithMode(datastar.ElementPatchModeInner),
				datastar.WithUseViewTransitions(false),
			)

		case allCompleted, ok := <-completedCh:
			if !ok {
				return
			}

			if s.isSameCompletedSnapshot(allCompleted) {
				continue
			}

			// Patch signals for counts
			sse.PatchSignals([]byte(fmt.Sprintf(`{"recent_completed_count": %d}`, len(allCompleted))))

			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}
			if err := tmpl.ExecuteTemplate(&buf, "recent-completed", map[string]any{
				"Jobs": allCompleted,
			}); err != nil {
				slog.Error("template render failed", "template", "recent-completed", "error", err)
			} else {
				sse.PatchElements(
					buf.String(),
					datastar.WithSelector("#recent-completed-list"),
					datastar.WithMode(datastar.ElementPatchModeInner),
					datastar.WithUseViewTransitions(false),
				)
			}

		case allFailures, ok := <-failuresCh:
			if !ok {
				return
			}

			// Patch signals for counts
			sse.PatchSignals([]byte(fmt.Sprintf(`{"recent_failures_count": %d}`, len(allFailures))))

			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}
			if err := tmpl.ExecuteTemplate(&buf, "recent-failures", map[string]any{
				"Failures": allFailures,
			}); err != nil {
				slog.Error("template render failed", "template", "recent-failures", "error", err)
			} else {
				sse.PatchElements(
					buf.String(),
					datastar.WithSelector("#recent-failures-list"),
					datastar.WithMode(datastar.ElementPatchModeInner),
					datastar.WithUseViewTransitions(false),
				)
			}

		case allMetrics, ok := <-rdsCh:
			if !ok {
				return
			}

			// Handle RDS updates
			sse.PatchSignals([]byte(fmt.Sprintf(`{"rds_loading": false, "rds_db_count": %d}`, len(allMetrics))))

			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}

			if err := tmpl.ExecuteTemplate(&buf, "rds-metrics", map[string]any{
				"Metrics": allMetrics,
			}); err != nil {
				slog.Error("template render failed", "template", "rds-metrics", "error", err)
			} else {
				sse.PatchElements(
					buf.String(),
					datastar.WithSelector("#rds-metrics-content"),
					datastar.WithMode(datastar.ElementPatchModeInner),
				)
			}

		case report, ok := <-lambdaCh:
			if !ok {
				return
			}

			// Check for credential errors
			hasCredError, credMsg, _ := s.getCredentialError()
			if hasCredError {
				sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, credMsg)))
				continue
			}

			// Handle Lambda updates
			sse.PatchSignals([]byte(fmt.Sprintf(`{"lambda_warnings": %d, "lambda_count": %d}`,
				report.WarningCount, len(report.Metrics))))

			tmpl := s.templateSet("index")
			if tmpl == nil {
				continue
			}

			// Render warnings section
			warnings := make([]*aws.LambdaMetrics, 0)
			for _, m := range report.Metrics {
				if m.TimeoutPercent >= aws.TimeoutWarningHigh {
					warnings = append(warnings, m)
				}
			}

			var warningsBuf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&warningsBuf, "lambda-warnings", map[string]any{
				"Warnings": warnings,
			}); err == nil {
				sse.PatchElements(warningsBuf.String(),
					datastar.WithSelector("#lambda-warnings-content"),
					datastar.WithMode(datastar.ElementPatchModeInner),
				)
			}

			// Render resources section
			var resourcesBuf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&resourcesBuf, "lambda-resources", map[string]any{
				"Metrics": report.Metrics,
			}); err == nil {
				sse.PatchElements(resourcesBuf.String(),
					datastar.WithSelector("#lambda-resources-content"),
					datastar.WithMode(datastar.ElementPatchModeInner),
				)
			}
		}
	}
}

func (s *Server) fetchRecentCompleted() ([]aws.Execution, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	since := time.Now().Add(-15 * time.Minute)
	var allCompleted []aws.Execution
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, client := range s.awsManager.Clients {
		wg.Add(1)
		go func(c *aws.Client) {
			defer wg.Done()
			execs, err := c.ListRecentCompleted(ctx, since)
			if err == nil {
				for i := range execs {
					if execs[i].ExecutionArn == "" {
						continue
					}
					desc, err := c.Sfn.DescribeExecution(ctx, &sfn.DescribeExecutionInput{
						ExecutionArn: &execs[i].ExecutionArn,
					})
					if err != nil {
						continue
					}
					bucket, key, prefix := extractBucketKeyPrefix(desc.Input)
					outBucket, outManifestKey := extractResultWriterDetails(desc.Output)
					outPrefix := prefix
					if outBucket != "" && outManifestKey != "" {
						outPrefix = strings.TrimSuffix(outManifestKey, path.Base(outManifestKey))
					}
					execs[i].InputBucket = bucket
					execs[i].InputKey = key
					execs[i].OutputBucket = outBucket
					execs[i].OutputPrefix = outPrefix
					execs[i].OutputManifestKey = outManifestKey
				}
				mu.Lock()
				allCompleted = append(allCompleted, execs...)
				mu.Unlock()
				return
			}
			slog.Warn("list recent completed failed", "env", c.EnvName, "err", err)
		}(client)
	}
	wg.Wait()

	sort.Slice(allCompleted, func(i, j int) bool {
		return allCompleted[i].StopTime.After(allCompleted[j].StopTime)
	})

	s.notifyMu.Lock()
	allCompleted = s.markNewExecutions(allCompleted, s.flashCompletedSeen)
	s.notifyMu.Unlock()

	if len(allCompleted) > 50 {
		allCompleted = allCompleted[:50]
	}

	return allCompleted, nil
}

func (s *Server) isSameCompletedSnapshot(execs []aws.Execution) bool {
	h := sha1.New()
	for _, e := range execs {
		h.Write([]byte(e.ExecutionArn))
		h.Write([]byte(e.Status))
		if !e.StopTime.IsZero() {
			h.Write([]byte(e.StopTime.UTC().Format(time.RFC3339Nano)))
		}
		h.Write([]byte("|"))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	s.completedRenderMu.Lock()
	defer s.completedRenderMu.Unlock()
	if sum == s.completedRenderHash {
		return true
	}
	s.completedRenderHash = sum
	return false
}

func (s *Server) fetchActiveExecutions() ([]aws.Execution, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var allActive []aws.Execution
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, client := range s.awsManager.Clients {
		wg.Add(1)
		go func(c *aws.Client) {
			defer wg.Done()

			interval := 30 * time.Second
			if s.cfg != nil && s.cfg.Polling.ActiveIntervalByEnv != nil {
				envKey := strings.ToLower(c.EnvName)
				if v, ok := s.cfg.Polling.ActiveIntervalByEnv[envKey]; ok {
					interval = v
				} else if base := baseEnvFromKey(envKey); base != "" {
					if v, ok := s.cfg.Polling.ActiveIntervalByEnv[base]; ok {
						interval = v
					}
				}
			}
			s.notifyActiveInterval(c.EnvName, interval)

			now := time.Now()
			s.activeCacheMu.Lock()
			last := s.activeCacheAt[c.EnvName]
			if !last.IsZero() && now.Sub(last) < interval {
				// reuse last snapshot for this env
				cached := markStaleExecutions(s.activeCache[c.EnvName])
				s.activeCacheMu.Unlock()
				if len(cached) > 0 {
					mu.Lock()
					allActive = append(allActive, cached...)
					mu.Unlock()
				}
				return
			}
			s.activeCacheAt[c.EnvName] = now
			s.activeCacheMu.Unlock()

			active, err := c.ListActiveExecutions(ctx)
			if err == nil {
				s.clearCredentialError() // Clear error on success
				s.activeCacheMu.Lock()
				s.activeCache[c.EnvName] = active
				s.activeCacheMu.Unlock()

				mu.Lock()
				allActive = append(allActive, active...)
				mu.Unlock()
				return
			}

			// Check if it's a credential error
			if aws.IsCredentialError(err) {
				s.setCredentialError(err)
			}

			slog.Warn("list active executions failed", "env", c.EnvName, "err", err)
			// fallback to last successful snapshot for this env
			s.activeCacheMu.Lock()
			cached := markStaleExecutions(s.activeCache[c.EnvName])
			s.activeCacheMu.Unlock()
			if len(cached) > 0 {
				mu.Lock()
				allActive = append(allActive, cached...)
				mu.Unlock()
			}
		}(client)
	}
	wg.Wait()

	sort.Slice(allActive, func(i, j int) bool {
		return allActive[i].StartTime.After(allActive[j].StartTime)
	})

	s.notifyMu.Lock()
	allActive = s.markNewExecutions(allActive, s.flashActiveSeen)
	s.notifyMu.Unlock()

	s.notifyNewActiveExecutions(allActive)

	return allActive, nil
}

func markStaleExecutions(src []aws.Execution) []aws.Execution {
	if len(src) == 0 {
		return nil
	}
	out := make([]aws.Execution, len(src))
	copy(out, src)
	for i := range out {
		out[i].Stale = true
	}
	return out
}

func (s *Server) fetchRecentFailures() ([]aws.Execution, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var allFailures []aws.Execution
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, client := range s.awsManager.Clients {
		wg.Add(1)
		go func(c *aws.Client) {
			defer wg.Done()
			failures, err := c.ListRecentFailures(ctx)
			if err == nil {
				mu.Lock()
				allFailures = append(allFailures, failures...)
				mu.Unlock()
				return
			}
			slog.Warn("list recent failures failed", "env", c.EnvName, "err", err)
		}(client)
	}
	wg.Wait()

	sort.Slice(allFailures, func(i, j int) bool {
		return allFailures[i].StopTime.After(allFailures[j].StopTime)
	})

	s.notifyMu.Lock()
	allFailures = s.markNewExecutions(allFailures, s.flashFailuresSeen)
	s.notifyMu.Unlock()

	s.notifyNewFailures(allFailures)

	return allFailures, nil
}

func (s *Server) fetchStateMachines() ([]StateMachineItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var items []StateMachineItem
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, client := range s.awsManager.Clients {
		wg.Add(1)
		go func(c *aws.Client) {
			defer wg.Done()
			// Use a shorter cache inside the client if possible, or just fetch
			list, err := c.ListFilteredStateMachines(ctx, 10*time.Minute)
			if err != nil {
				slog.Warn("list filtered state machines failed", "env", c.EnvName, "err", err)
				return
			}
			localItems := make([]StateMachineItem, 0, len(list))
			for _, sm := range list {
				localItems = append(localItems, StateMachineItem{
					Env:     c.EnvName,
					BaseEnv: baseEnvFromKey(strings.ToLower(c.EnvName)),
					Name:    derefString(sm.Name),
					Arn:     derefString(sm.StateMachineArn),
				})
			}
			mu.Lock()
			items = append(items, localItems...)
			mu.Unlock()
		}(client)
	}
	wg.Wait()

	sort.Slice(items, func(i, j int) bool {
		if items[i].Env == items[j].Env {
			return items[i].Name < items[j].Name
		}
		return items[i].Env < items[j].Env
	})

	return items, nil
}

// sendCachedRDSMetrics sends any cached RDS metrics immediately on SSE connection
func (s *Server) sendCachedRDSMetrics(sse *datastar.ServerSentEventGenerator) {
	s.rdsCacheMu.Lock()
	defer s.rdsCacheMu.Unlock()

	if s.rdsCache == nil || len(s.rdsCache) == 0 {
		return
	}

	// Collect all cached metrics
	var allMetrics []aws.RDSMetric
	for _, cached := range s.rdsCache {
		allMetrics = append(allMetrics, cached...)
	}

	if len(allMetrics) == 0 {
		return
	}

	// Update signals
	sse.PatchSignals([]byte(fmt.Sprintf(`{"rds_loading": false, "rds_db_count": %d}`, len(allMetrics))))

	// Render template
	tmpl := s.templateSet("index")
	if tmpl != nil {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "rds-metrics", map[string]any{
			"Metrics": allMetrics,
		}); err == nil {
			sse.PatchElements(buf.String(),
				datastar.WithSelector("#rds-metrics-content"),
				datastar.WithMode(datastar.ElementPatchModeInner),
			)
		}
	}
}

// sendCachedLambdaMetrics sends any cached Lambda metrics immediately on SSE connection
func (s *Server) sendCachedLambdaMetrics(sse *datastar.ServerSentEventGenerator) {
	s.lambdaCacheMu.Lock()
	defer s.lambdaCacheMu.Unlock()

	if len(s.lambdaCache) == 0 {
		return
	}

	// Collect all cached metrics
	var allMetrics []*aws.LambdaMetrics
	for _, cached := range s.lambdaCache {
		allMetrics = append(allMetrics, cached.Metrics)
	}

	if len(allMetrics) == 0 {
		return
	}

	// Sort by timeout percentage
	sort.Slice(allMetrics, func(i, j int) bool {
		return allMetrics[i].TimeoutPercent > allMetrics[j].TimeoutPercent
	})

	// Count warnings
	warningCount := 0
	for _, m := range allMetrics {
		if m.TimeoutPercent >= aws.TimeoutWarningHigh {
			warningCount++
		}
	}

	// Update signals
	signals := fmt.Sprintf(`{"lambda_warnings": %d, "lambda_count": %d}`,
		warningCount, len(allMetrics))
	sse.PatchSignals([]byte(signals))

	// Render warnings
	tmpl := s.templateSet("index")
	if tmpl != nil {
		warnings := make([]*aws.LambdaMetrics, 0)
		for _, m := range allMetrics {
			if m.TimeoutPercent >= aws.TimeoutWarningHigh {
				warnings = append(warnings, m)
			}
		}

		var warningsBuf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&warningsBuf, "lambda-warnings", map[string]any{
			"Warnings": warnings,
		}); err == nil {
			sse.PatchElements(warningsBuf.String(),
				datastar.WithSelector("#lambda-warnings-content"),
				datastar.WithMode(datastar.ElementPatchModeInner),
			)
		}

		// Render resources
		var resourcesBuf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&resourcesBuf, "lambda-resources", map[string]any{
			"Metrics": allMetrics,
		}); err == nil {
			sse.PatchElements(resourcesBuf.String(),
				datastar.WithSelector("#lambda-resources-content"),
				datastar.WithMode(datastar.ElementPatchModeInner),
			)
		}
	}
}

// setCredentialError records a credential error for display in UI
func (s *Server) setCredentialError(err error) {
	s.credentialErrorMu.Lock()
	defer s.credentialErrorMu.Unlock()
	s.credentialError = true
	s.credentialErrorMsg = err.Error()
	s.credentialErrorAt = time.Now()
}

// clearCredentialError clears the credential error state
func (s *Server) clearCredentialError() {
	s.credentialErrorMu.Lock()
	defer s.credentialErrorMu.Unlock()
	s.credentialError = false
	s.credentialErrorMsg = ""
}

// getCredentialError returns the current credential error state
func (s *Server) getCredentialError() (bool, string, time.Time) {
	s.credentialErrorMu.RLock()
	defer s.credentialErrorMu.RUnlock()
	return s.credentialError, s.credentialErrorMsg, s.credentialErrorAt
}

// getActiveExecutionsCount returns the total number of active executions across all environments
func (s *Server) getActiveExecutionsCount() int {
	s.activeCacheMu.Lock()
	defer s.activeCacheMu.Unlock()

	count := 0
	for _, execs := range s.activeCache {
		count += len(execs)
	}
	return count
}

func (s *Server) notifyActiveInterval(env string, interval time.Duration) {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	last, ok := s.activeIntervalLogged[env]
	if ok && last == interval {
		return
	}
	s.activeIntervalLogged[env] = interval
	slog.Info("active polling interval", "env", env, "interval", interval)
}

func (s *Server) markNewExecutions(execs []aws.Execution, seen map[string]time.Time) []aws.Execution {
	if len(execs) == 0 {
		return execs
	}
	now := time.Now()
	for i := range execs {
		key := execs[i].ExecutionArn
		if key == "" {
			key = execs[i].Env + "|" + execs[i].Name + "|" + execs[i].ExecutionName
		}
		if _, ok := seen[key]; !ok {
			execs[i].New = true
		}
		seen[key] = now
	}
	return execs
}
