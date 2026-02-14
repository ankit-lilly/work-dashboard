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

	activeCh := s.activeBroadcaster.Subscribe()
	defer s.activeBroadcaster.Unsubscribe(activeCh)

	completedCh := s.completedBroadcaster.Subscribe()
	defer s.completedBroadcaster.Unsubscribe(completedCh)

	failuresCh := s.failuresBroadcaster.Subscribe()
	defer s.failuresBroadcaster.Unsubscribe(failuresCh)

	// Note: State machines are loaded on initial page render, no SSE update needed
	//Update different sections via SSE as data comes in.

	for {
		select {
		case <-ctx.Done():
			return
		case allActive, ok := <-activeCh:
			if !ok {
				return
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
				s.activeCacheMu.Lock()
				s.activeCache[c.EnvName] = active
				s.activeCacheMu.Unlock()

				mu.Lock()
				allActive = append(allActive, active...)
				mu.Unlock()
				return
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
