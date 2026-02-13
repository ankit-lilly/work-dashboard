package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/starfederation/datastar-go/datastar"
)

type RecordSearchResult struct {
	Env           string
	StateMachine  string
	ExecutionName string
	ExecutionArn  string
	StartTime     string
	Bucket        string
	Key           string
	Index         int
	Record        string
	ChildArn      string
	ChildName     string
	OutputBucket  string
	OutputPrefix  string
	OutputKey     string
}

type searchUpdate struct {
	newResults []RecordSearchResult
	err        error
	done       bool
}

type searchState struct {
	mu        sync.Mutex
	running   bool
	done      bool
	canceled  bool
	results   []RecordSearchResult
	seen      map[string]struct{}
	listeners map[chan searchUpdate]struct{}
}

func (s *Server) handleRecordSearch(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	email := strings.TrimSpace(r.FormValue("email_address"))
	env := strings.TrimSpace(r.FormValue("env"))
	stateMachine := strings.TrimSpace(r.FormValue("state_machine"))
	startDate := strings.TrimSpace(r.FormValue("start_date"))
	endDate := strings.TrimSpace(r.FormValue("end_date"))

	startAt, endAt, err := parseDateRange(startDate, endDate)
	if err != nil {
		var buf bytes.Buffer
		tmpl := s.templateSet("index")
		if tmpl != nil {
			_ = tmpl.ExecuteTemplate(&buf, "record-search-results", map[string]any{
				"Email":     email,
				"Env":       env,
				"SearchKey": "",
				"Results":   nil,
				"Error":     err.Error(),
				"Searching": false,
				"Stopped":   false,
			})
			sse.PatchElements(buf.String())
		}
		return
	}

	key := buildSearchKey(email, env, stateMachine, startDate, endDate)
	state := s.getOrCreateSearchState(key)

	// Initial render (shell + existing rows)
	{
		var buf bytes.Buffer
		tmpl := s.templateSet("index")
		if tmpl == nil {
			return
		}
		state.mu.Lock()
		existing := make([]RecordSearchResult, len(state.results))
		copy(existing, state.results)
		searching := email != "" && !state.done && !state.canceled
		state.mu.Unlock()

		_ = tmpl.ExecuteTemplate(&buf, "record-search-results", map[string]any{
			"Email":     email,
			"Env":       env,
			"SearchKey": key,
			"Results":   existing,
			"Error":     nil,
			"Searching": searching,
		})
		sse.PatchElements(buf.String())
	}

	if email == "" {
		return
	}

	state.mu.Lock()
	shouldStart := !state.running && !state.done && !state.canceled
	if shouldStart {
		state.running = true
	}
	state.mu.Unlock()

	if shouldStart {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			s.searchEmailStreaming(ctx, email, env, stateMachine, startAt, endAt, state)
		}()
	}

	ch := state.subscribe()
	defer state.unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case upd, ok := <-ch:
			if !ok {
				return
			}

			if len(upd.newResults) > 0 {
				var buf bytes.Buffer
				tmpl := s.templateSet("index")
				if tmpl == nil {
					return
				}
				_ = tmpl.ExecuteTemplate(&buf, "record-search-rows", map[string]any{
					"Results": upd.newResults,
				})
				sse.PatchElements(
					buf.String(),
					datastar.WithSelector("#record-search-rows"),
					datastar.WithMode(datastar.ElementPatchModeAppend),
				)
			}

			var statusBuf bytes.Buffer
			tmpl := s.templateSet("index")
			if tmpl == nil {
				return
			}
			_ = tmpl.ExecuteTemplate(&statusBuf, "record-search-status", map[string]any{
				"Searching": !upd.done,
				"Email":     email,
				"Stopped":   false,
			})
			sse.PatchElements(
				statusBuf.String(),
				datastar.WithSelector("#record-search-status"),
				datastar.WithMode(datastar.ElementPatchModeOuter),
			)

			if upd.done {
				keepAlive(r.Context(), w)
				return
			}
		}
	}
}

func keepAlive(ctx context.Context, w http.ResponseWriter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) handleRecordSearchCancel(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		return
	}

	state := s.getOrCreateSearchState(key)
	state.mu.Lock()
	state.canceled = true
	state.running = false
	state.done = true
	state.mu.Unlock()
	state.broadcast(searchUpdate{done: true})

	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}
	_ = tmpl.ExecuteTemplate(&buf, "record-search-status", map[string]any{
		"Searching": false,
		"Email":     "",
		"Stopped":   true,
	})
	sse.PatchElements(
		buf.String(),
		datastar.WithSelector("#record-search-status"),
		datastar.WithMode(datastar.ElementPatchModeOuter),
	)
}

func (s *Server) searchEmailStreaming(ctx context.Context, email, env, stateMachine string, startAt, endAt *time.Time, state *searchState) {
	defer func() {
		state.mu.Lock()
		state.running = false
		state.done = true
		state.mu.Unlock()
		state.broadcast(searchUpdate{done: true})
	}()

	cutoff := time.Time{}
	if startAt != nil {
		cutoff = *startAt
	}
	maxResults := 20
	if startAt != nil || endAt != nil {
		maxResults = 100
	}

	for _, client := range s.awsManager.Clients {
		if !envMatchesSelection(client.EnvName, env) {
			continue
		}

		stateMachines, err := client.ListFilteredStateMachines(ctx, 10*time.Minute)
		if err != nil {
			if !isContextCanceled(err) {
				slog.Warn("list filtered state machines failed", "env", client.EnvName, "err", err)
			}
			continue
		}

		const batchSize = 4
		const batchDelay = 400 * time.Millisecond

		for i := 0; i < len(stateMachines); i += batchSize {
			if ctx.Err() != nil || state.isCanceled() {
				return
			}

			end := i + batchSize
			if end > len(stateMachines) {
				end = len(stateMachines)
			}

			for _, sm := range stateMachines[i:end] {
				if !matchesStateMachineFilter(sm, stateMachine) {
					continue
				}
				execs, err := listRecentExecutionsForSearch(ctx, client, sm.StateMachineArn, cutoff, maxResults)
				if err != nil {
					if !isContextCanceled(err) {
						slog.Warn("list recent executions failed", "env", client.EnvName, "state_machine_arn", derefString(sm.StateMachineArn), "err", err)
					}
					continue
				}

				var newResults []RecordSearchResult
				for _, e := range execs {
					if e.StartDate == nil {
						continue
					}
					if startAt != nil && e.StartDate.Before(*startAt) {
						continue
					}
					if endAt != nil && e.StartDate.After(*endAt) {
						continue
					}
					desc, err := client.Sfn.DescribeExecution(ctx, &sfn.DescribeExecutionInput{
						ExecutionArn: e.ExecutionArn,
					})
					if err != nil {
						continue
					}

					bucket, key := extractBucketKey(desc.Input)
					if bucket == "" || key == "" {
						continue
					}
					outBucket, outKey := extractResultWriterDetails(desc.Output)
					outPrefix := ""
					if outBucket != "" && outKey != "" {
						outPrefix = strings.TrimSuffix(outKey, path.Base(outKey))
					}

					match, err := client.FindEmailInS3JSON(ctx, bucket, key, email)
					if err != nil || match == nil {
						continue
					}

					res := RecordSearchResult{
						Env:           client.EnvName,
						StateMachine:  derefString(sm.Name),
						ExecutionName: derefString(e.Name),
						ExecutionArn:  derefString(e.ExecutionArn),
						StartTime:     formatTime(e.StartDate),
						Bucket:        bucket,
						Key:           key,
						Index:         match.Index,
						Record:        match.Record,
						OutputBucket:  outBucket,
						OutputPrefix:  outPrefix,
						OutputKey:     outKey,
					}

					if state.addResult(res) {
						newResults = append(newResults, res)
					}
				}

				if len(newResults) > 0 {
					state.broadcast(searchUpdate{newResults: newResults})
				}
			}

			if end < len(stateMachines) {
				select {
				case <-ctx.Done():
					return
				default:
				case <-time.After(batchDelay):
				}
			}
		}
	}
}

func (s *Server) getOrCreateSearchState(key string) *searchState {
	if key == "" {
		return &searchState{}
	}
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if st, ok := s.searchStates[key]; ok {
		return st
	}
	st := &searchState{
		seen:      make(map[string]struct{}),
		listeners: make(map[chan searchUpdate]struct{}),
	}
	s.searchStates[key] = st
	return st
}

func (s *searchState) addResult(res RecordSearchResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := res.ExecutionArn + "|" + strconv.Itoa(res.Index)
	if _, ok := s.seen[key]; ok {
		return false
	}
	s.seen[key] = struct{}{}
	s.results = append(s.results, res)
	return true
}

func (s *searchState) broadcast(update searchUpdate) {
	s.mu.Lock()
	for ch := range s.listeners {
		select {
		case ch <- update:
		default:
		}
	}
	s.mu.Unlock()
}

func (s *searchState) subscribe() chan searchUpdate {
	ch := make(chan searchUpdate, 4)
	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *searchState) unsubscribe(ch chan searchUpdate) {
	s.mu.Lock()
	delete(s.listeners, ch)
	s.mu.Unlock()
	close(ch)
}

func (s *searchState) isCanceled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canceled
}

func listRecentExecutionsForSearch(ctx context.Context, c *aws.Client, stateMachineArn *string, cutoff time.Time, maxResults int) ([]types.ExecutionListItem, error) {
	statuses := []types.ExecutionStatus{
		types.ExecutionStatusRunning,
		types.ExecutionStatusFailed,
		types.ExecutionStatusSucceeded,
	}

	if maxResults <= 0 {
		maxResults = 30
	}
	out := make([]types.ExecutionListItem, 0, maxResults)
	for _, status := range statuses {
		execs, err := c.ListRecentExecutionsByStatus(ctx, stateMachineArn, status, cutoff, 2)
		if err != nil {
			return nil, err
		}
		for _, e := range execs {
			out = append(out, e)
			if len(out) >= maxResults {
				return out, nil
			}
		}
	}

	return out, nil
}

func matchesStateMachineFilter(sm types.StateMachineListItem, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	name := strings.ToLower(derefString(sm.Name))
	arn := strings.ToLower(derefString(sm.StateMachineArn))
	return strings.Contains(name, filter) || strings.Contains(arn, filter)
}

func extractBucketKey(input *string) (string, string) {
	b, k, _ := extractBucketKeyPrefix(input)
	return b, k
}

func extractBucketKeyPrefix(input *string) (string, string, string) {
	if input == nil || strings.TrimSpace(*input) == "" {
		return "", "", ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(*input), &payload); err != nil {
		return "", "", ""
	}

	if b, k, p := getBucketKeyPrefix(payload); b != "" {
		return b, k, p
	}

	if p, ok := payload["Payload"].(map[string]any); ok {
		return getBucketKeyPrefix(p)
	}

	return "", "", ""
}

func getBucketKeyPrefix(m map[string]any) (string, string, string) {
	bucket, _ := m["Bucket"].(string)
	key, _ := m["Key"].(string)
	prefix, _ := m["Prefix"].(string)
	if bucket == "" {
		return "", "", ""
	}
	return bucket, key, prefix
}

func parseDateRange(startDate, endDate string) (*time.Time, *time.Time, error) {
	const layout = "2006-01-02"
	var startAt *time.Time
	var endAt *time.Time

	if startDate != "" {
		t, err := time.ParseInLocation(layout, startDate, time.Local)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid start date")
		}
		startAt = &t
	}

	if endDate != "" {
		t, err := time.ParseInLocation(layout, endDate, time.Local)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid end date")
		}
		t = t.Add(24*time.Hour - time.Nanosecond)
		endAt = &t
	}

	if startAt != nil && endAt != nil && endAt.Before(*startAt) {
		return nil, nil, fmt.Errorf("end date must be on or after start date")
	}

	return startAt, endAt, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.In(time.Local).Format("2006-01-02 15:04:05 MST")
}

func isContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func buildSearchKey(email, env, stateMachine, startDate, endDate string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	env = strings.ToLower(strings.TrimSpace(env))
	stateMachine = strings.ToLower(strings.TrimSpace(stateMachine))
	startDate = strings.TrimSpace(startDate)
	endDate = strings.TrimSpace(endDate)
	var b strings.Builder
	b.Grow(len(email) + len(env) + len(stateMachine) + len(startDate) + len(endDate) + 16)
	b.WriteString(email)
	b.WriteByte('|')
	b.WriteString(env)
	b.WriteByte('|')
	b.WriteString(stateMachine)
	b.WriteByte('|')
	b.WriteString(startDate)
	b.WriteByte('|')
	b.WriteString(endDate)
	return b.String()
}
