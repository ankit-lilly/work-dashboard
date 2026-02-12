package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
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

	failuresCh := s.failuresBroadcaster.Subscribe()
	defer s.failuresBroadcaster.Unsubscribe(failuresCh)

	smCh := s.stateMachinesBroadcaster.Subscribe()
	defer s.stateMachinesBroadcaster.Unsubscribe(smCh)

	// Initial fetch for state machines
	go func() {
		items, err := s.fetchStateMachines()
		if err == nil {
			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl != nil {
				_ = tmpl.ExecuteTemplate(&buf, "state-machines-list", map[string]any{
					"Items": items,
				})
				sse.PatchElements(
					buf.String(),
					datastar.WithSelector("#state-machines-list"),
					datastar.WithMode(datastar.ElementPatchModeInner),
				)
			}
		}
	}()
	//Update different sections via SSE as data comes in.

	for {
		select {
		case <-ctx.Done():
			return
		case allActive, ok := <-activeCh:
			if !ok {
				return
			}

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
			)

		case allFailures, ok := <-failuresCh:
			if !ok {
				return
			}

			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}
			_ = tmpl.ExecuteTemplate(&buf, "recent-failures", map[string]any{
				"Failures": allFailures,
			})

			sse.PatchElements(
				buf.String(),
				datastar.WithSelector("#recent-failures-list"),
				datastar.WithMode(datastar.ElementPatchModeInner),
			)

		case allSMs, ok := <-smCh:
			if !ok {
				return
			}
			var buf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}
			_ = tmpl.ExecuteTemplate(&buf, "state-machines-list", map[string]any{
				"Items": allSMs,
			})
			sse.PatchElements(
				buf.String(),
				datastar.WithSelector("#state-machines-list"),
				datastar.WithMode(datastar.ElementPatchModeInner),
			)
		}
	}
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
			log.Printf("ListActiveExecutions failed for %s: %v", c.EnvName, err)
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
			log.Printf("ListRecentFailures failed for %s: %v", c.EnvName, err)
		}(client)
	}
	wg.Wait()

	sort.Slice(allFailures, func(i, j int) bool {
		return allFailures[i].StopTime.After(allFailures[j].StopTime)
	})

	s.notifyNewFailures(allFailures)

	return allFailures, nil
}

const jeffDeanFactsURL = "https://raw.githubusercontent.com/LRitzdorf/TheJeffDeanFacts/refs/heads/main/README.md"
const chuckNorrisURL = "https://api.chucknorris.io/jokes/random"

type chuckJokeResponse struct {
	Value string `json:"value"`
}

func (s *Server) getFunJoke(ctx context.Context) string {
	// 50/50 split, fallback to the other source on failure.
	if rand.Intn(2) == 0 {
		if joke := s.getJeffDeanFact(ctx); joke != "" {
			return joke
		}
		return s.getChuckNorrisJoke(ctx)
	}
	if joke := s.getChuckNorrisJoke(ctx); joke != "" {
		return joke
	}
	return s.getJeffDeanFact(ctx)
}

func (s *Server) getChuckNorrisJoke(ctx context.Context) string {
	s.jokeMu.Lock()
	if s.chuckJoke != "" && time.Since(s.chuckJokeAt) < 30*time.Minute {
		joke := s.chuckJoke
		s.jokeMu.Unlock()
		return joke
	}
	s.jokeMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, chuckNorrisURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}

	var payload chuckJokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	joke := strings.TrimSpace(payload.Value)
	if joke == "" {
		return ""
	}

	s.jokeMu.Lock()
	s.chuckJoke = joke
	s.chuckJokeAt = time.Now()
	s.jokeMu.Unlock()

	return joke
}

func (s *Server) getJeffDeanFact(ctx context.Context) string {
	s.jokeMu.Lock()
	if s.jeffJoke != "" && time.Since(s.jeffJokeAt) < 30*time.Minute {
		joke := s.jeffJoke
		s.jokeMu.Unlock()
		return joke
	}
	s.jokeMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, jeffDeanFactsURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	facts, err := parseJeffDeanFacts(string(body))
	if err != nil || len(facts) == 0 {
		return ""
	}

	rand.Seed(time.Now().UnixNano())
	joke := strings.TrimSpace(facts[rand.Intn(len(facts))])
	if joke == "" {
		return ""
	}

	s.jokeMu.Lock()
	s.jeffJoke = joke
	s.jeffJokeAt = time.Now()
	s.jokeMu.Unlock()

	return joke
}

func parseJeffDeanFacts(md string) ([]string, error) {
	start := strings.Index(md, "## The Facts")
	if start == -1 {
		return nil, fmt.Errorf("facts section not found")
	}
	md = md[start+len("## The Facts"):]
	if end := strings.Index(md, "\n## "); end != -1 {
		md = md[:end]
	}
	lines := strings.Split(md, "\n")
	facts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			fact := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if fact != "" {
				facts = append(facts, fact)
			}
		}
	}
	if len(facts) == 0 {
		return nil, fmt.Errorf("no facts found")
	}
	return facts, nil
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
				log.Printf("ListFilteredStateMachines failed for %s: %v", c.EnvName, err)
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
