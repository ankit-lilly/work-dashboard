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

type searchParams struct {
	email        string
	env          string
	stateMachine string
	startAt      *time.Time
	endAt        *time.Time
}

type searchUpdate struct {
	newResults []RecordSearchResult
	err        error
	done       bool
	progress   *searchProgress
}

type searchProgress struct {
	Env          string
	StateMachine string
	Checked      int
	Total        int
}

type searchState struct {
	mu           sync.Mutex
	running      bool
	done         bool
	canceled     bool
	results      []RecordSearchResult
	seen         map[string]struct{}
	listeners    map[chan searchUpdate]struct{}
	lastActivity time.Time
}

func (s *Server) handleRecordSearch(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)

	params, err := s.parseSearchParams(r)
	if err != nil {
		s.renderSearchError(sse, params.email, params.env, err)
		return
	}

	state := s.getOrCreateSearchState(buildSearchKey(params))
	s.renderInitialSearchResults(sse, params, state)

	if params.email == "" {
		return
	}

	s.startSearchIfNeeded(r.Context(), params, state)
	s.streamSearchUpdates(r.Context(), w, sse, params.email, state)
}

func (s *Server) handleRecordSearchCancel(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		return
	}

	state := s.getOrCreateSearchState(key)
	state.cancel()
	s.renderSearchStopped(sse)
}

// searchAllEnvironments searches for an email across all matching environments in parallel.
func (s *Server) searchAllEnvironments(ctx context.Context, params searchParams, state *searchState) {
	defer func() {
		slog.Info("search completed", "email", params.email)
		state.markDone()
	}()

	clients := s.getMatchingClients(params.env)
	slog.Info("search starting", "email", params.email, "env_filter", params.env, "client_count", len(clients))

	if len(clients) == 0 {
		slog.Warn("no matching clients found for search", "env_filter", params.env)
		return
	}

	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(c *aws.Client) {
			defer wg.Done()
			s.searchEnvironment(ctx, c, params, state)
		}(client)
	}
	wg.Wait()
}

// searchEnvironment searches all RulesProcessor state machines in a single environment.
func (s *Server) searchEnvironment(ctx context.Context, client *aws.Client, params searchParams, state *searchState) {
	stateMachines := s.getRulesProcessorStateMachines(ctx, client, params.stateMachine)
	slog.Info("searching environment", "env", client.EnvName, "rules_processor_count", len(stateMachines))

	if len(stateMachines) == 0 {
		return
	}

	var wg sync.WaitGroup
	for i, sm := range stateMachines {
		if state.isCanceled() || ctx.Err() != nil {
			slog.Warn("search canceled or context done", "env", client.EnvName, "canceled", state.isCanceled(), "ctx_err", ctx.Err())
			return
		}

		wg.Add(1)
		go func(sm types.StateMachineListItem, index int) {
			defer wg.Done()
			s.searchStateMachine(ctx, client, sm, params, state, index, len(stateMachines))
		}(sm, i)
	}
	wg.Wait()
}

// searchStateMachine searches all executions of a single state machine for the email.
func (s *Server) searchStateMachine(ctx context.Context, client *aws.Client, sm types.StateMachineListItem, params searchParams, state *searchState, index, total int) {
	if state.isCanceled() || ctx.Err() != nil {
		return
	}

	smName := derefString(sm.Name)
	state.broadcastProgress(client.EnvName, smName, index, total)

	executions := s.listAllExecutions(ctx, client, sm.StateMachineArn, params.startAt)

	// Log date range of executions found
	if len(executions) > 0 {
		oldest := executions[len(executions)-1]
		newest := executions[0]
		slog.Info("searching state machine",
			"env", client.EnvName,
			"sm", smName,
			"execution_count", len(executions),
			"newest", formatTime(newest.StartDate),
			"oldest", formatTime(oldest.StartDate))
	} else {
		slog.Info("searching state machine", "env", client.EnvName, "sm", smName, "execution_count", 0)
	}

	s.searchExecutionsForEmail(ctx, client, executions, smName, params, state)
}

func (s *Server) searchExecutionsForEmail(ctx context.Context, client *aws.Client, executions []types.ExecutionListItem, smName string, params searchParams, state *searchState) {
	checkedCount := 0
	for _, exec := range executions {
		if state.isCanceled() || ctx.Err() != nil {
			return
		}

		if !s.executionInDateRange(exec, params.startAt, params.endAt) {
			continue
		}

		checkedCount++
		result := s.checkExecutionForEmail(ctx, client, exec, smName, params.email)
		if result != nil && state.addResult(*result) {
			slog.Info("found match", "env", client.EnvName, "sm", smName, "execution", result.ExecutionName, "index", result.Index)
			state.broadcastResult(*result)
		}
	}
	slog.Debug("finished searching executions", "env", client.EnvName, "sm", smName, "checked", checkedCount)
}

func (s *Server) getMatchingClients(envFilter string) []*aws.Client {
	var clients []*aws.Client
	for _, client := range s.awsManager.Clients {
		if envMatchesSelection(client.EnvName, envFilter) {
			clients = append(clients, client)
		}
	}
	return clients
}

func (s *Server) getRulesProcessorStateMachines(ctx context.Context, client *aws.Client, filter string) []types.StateMachineListItem {
	allMachines, err := client.ListFilteredStateMachines(ctx, 10*time.Minute)
	if err != nil {
		if !isContextCanceled(err) {
			slog.Warn("list state machines failed", "env", client.EnvName, "err", err)
		}
		return nil
	}

	var rulesProcessors []types.StateMachineListItem
	for _, sm := range allMachines {
		name := derefString(sm.Name)
		if strings.Contains(name, "RulesProcessor") && matchesFilter(sm, filter) {
			rulesProcessors = append(rulesProcessors, sm)
		}
	}
	return rulesProcessors
}

func (s *Server) listAllExecutions(ctx context.Context, client *aws.Client, arn *string, startAt *time.Time) []types.ExecutionListItem {
	cutoff := time.Time{}
	if startAt != nil {
		cutoff = *startAt
	}

	execs, err := listExecutionsUnlimited(ctx, client, arn, cutoff)
	if err != nil {
		if !isContextCanceled(err) {
			slog.Warn("list executions failed", "arn", derefString(arn), "err", err)
		}
		return nil
	}
	return execs
}

func (s *Server) executionInDateRange(exec types.ExecutionListItem, startAt, endAt *time.Time) bool {
	if exec.StartDate == nil {
		return false
	}
	if startAt != nil && exec.StartDate.Before(*startAt) {
		return false
	}
	if endAt != nil && exec.StartDate.After(*endAt) {
		return false
	}
	return true
}

func (s *Server) checkExecutionForEmail(ctx context.Context, client *aws.Client, exec types.ExecutionListItem, smName, email string) *RecordSearchResult {
	execName := derefString(exec.Name)

	desc, err := client.Sfn.DescribeExecution(ctx, &sfn.DescribeExecutionInput{
		ExecutionArn: exec.ExecutionArn,
	})
	if err != nil {
		return nil
	}

	bucket, key := extractBucketKey(desc.Input)
	if bucket == "" || key == "" {
		// Log first 200 chars of input to debug extraction
		inputPreview := ""
		if desc.Input != nil && len(*desc.Input) > 0 {
			inputPreview = *desc.Input
			if len(inputPreview) > 200 {
				inputPreview = inputPreview[:200] + "..."
			}
		}
		slog.Info("no bucket/key extracted", "exec", execName, "input_preview", inputPreview)
		return nil
	}

	match, err := client.FindEmailInS3JSON(ctx, bucket, key, email)
	if err != nil {
		slog.Warn("find email error", "exec", execName, "bucket", bucket, "key", key, "err", err)
		return nil
	}
	if match == nil {
		return nil
	}

	outBucket, outKey := extractResultWriterDetails(desc.Output)
	outPrefix := ""
	if outBucket != "" && outKey != "" {
		outPrefix = strings.TrimSuffix(outKey, path.Base(outKey))
	}

	return &RecordSearchResult{
		Env:           client.EnvName,
		StateMachine:  smName,
		ExecutionName: derefString(exec.Name),
		ExecutionArn:  derefString(exec.ExecutionArn),
		StartTime:     formatTime(exec.StartDate),
		Bucket:        bucket,
		Key:           key,
		Index:         match.Index,
		Record:        match.Record,
		OutputBucket:  outBucket,
		OutputPrefix:  outPrefix,
		OutputKey:     outKey,
	}
}

func listExecutionsUnlimited(ctx context.Context, c *aws.Client, arn *string, cutoff time.Time) ([]types.ExecutionListItem, error) {
	statuses := []types.ExecutionStatus{
		types.ExecutionStatusRunning,
		types.ExecutionStatusFailed,
		types.ExecutionStatusSucceeded,
	}

	const maxPages = 50
	var results []types.ExecutionListItem

	for _, status := range statuses {
		execs, err := c.ListRecentExecutionsByStatus(ctx, arn, status, cutoff, maxPages)
		if err != nil {
			return nil, err
		}
		results = append(results, execs...)
	}

	return results, nil
}

func matchesFilter(sm types.StateMachineListItem, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	name := strings.ToLower(derefString(sm.Name))
	arn := strings.ToLower(derefString(sm.StateMachineArn))
	return strings.Contains(name, filter) || strings.Contains(arn, filter)
}

func (s *Server) parseSearchParams(r *http.Request) (searchParams, error) {
	if err := r.ParseForm(); err != nil {
		return searchParams{}, err
	}

	email := strings.TrimSpace(r.FormValue("email_address"))
	env := strings.TrimSpace(r.FormValue("env"))
	stateMachine := strings.TrimSpace(r.FormValue("state_machine"))
	startDate := strings.TrimSpace(r.FormValue("start_date"))
	endDate := strings.TrimSpace(r.FormValue("end_date"))

	startAt, endAt, err := parseDateRange(startDate, endDate)
	if err != nil {
		return searchParams{email: email, env: env}, err
	}

	return searchParams{
		email:        email,
		env:          env,
		stateMachine: stateMachine,
		startAt:      startAt,
		endAt:        endAt,
	}, nil
}

func (s *Server) renderSearchError(sse *datastar.ServerSentEventGenerator, email, env string, err error) {
	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}

	if err := tmpl.ExecuteTemplate(&buf, "record-search-results", map[string]any{
		"Email":     email,
		"Env":       env,
		"SearchKey": "",
		"Results":   nil,
		"Error":     err.Error(),
		"Searching": false,
		"Stopped":   false,
	}); err != nil {
		slog.Error("template render failed", "template", "record-search-results", "error", err)
		return
	}
	sse.PatchElements(buf.String(), datastar.WithUseViewTransitions(false))
}

func (s *Server) renderInitialSearchResults(sse *datastar.ServerSentEventGenerator, params searchParams, state *searchState) {
	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}

	state.mu.Lock()
	existing := make([]RecordSearchResult, len(state.results))
	copy(existing, state.results)
	searching := params.email != "" && !state.done && !state.canceled
	state.mu.Unlock()

	if err := tmpl.ExecuteTemplate(&buf, "record-search-results", map[string]any{
		"Email":     params.email,
		"Env":       params.env,
		"SearchKey": buildSearchKey(params),
		"Results":   existing,
		"Error":     nil,
		"Searching": searching,
	}); err != nil {
		slog.Error("template render failed", "template", "record-search-results", "error", err)
		return
	}
	sse.PatchElements(buf.String())
}

func (s *Server) renderSearchStopped(sse *datastar.ServerSentEventGenerator) {
	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}

	if err := tmpl.ExecuteTemplate(&buf, "record-search-status", map[string]any{
		"Searching": false,
		"Email":     "",
		"Stopped":   true,
	}); err != nil {
		slog.Error("template render failed", "template", "record-search-status", "error", err)
		return
	}
	sse.PatchElements(buf.String(),
		datastar.WithSelector("#record-search-status"),
		datastar.WithMode(datastar.ElementPatchModeOuter),
	)
}

func (s *Server) startSearchIfNeeded(ctx context.Context, params searchParams, state *searchState) {
	state.mu.Lock()
	shouldStart := !state.running && !state.done && !state.canceled
	slog.Info("startSearchIfNeeded", "shouldStart", shouldStart, "running", state.running, "done", state.done, "canceled", state.canceled)
	if shouldStart {
		state.running = true
	}
	state.mu.Unlock()

	if shouldStart {
		go s.searchAllEnvironments(ctx, params, state)
	}
}

func (s *Server) streamSearchUpdates(ctx context.Context, w http.ResponseWriter, sse *datastar.ServerSentEventGenerator, email string, state *searchState) {
	ch := state.subscribe()
	defer state.unsubscribe(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-ch:
			if !ok {
				return
			}

			s.renderSearchUpdate(sse, email, update)

			if update.done {
				keepAlive(ctx, w)
				return
			}
		}
	}
}

func (s *Server) renderSearchUpdate(sse *datastar.ServerSentEventGenerator, email string, update searchUpdate) {
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}

	// Render new results
	if len(update.newResults) > 0 {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "record-search-rows", map[string]any{
			"Results": update.newResults,
		}); err != nil {
			slog.Error("template render failed", "template", "record-search-rows", "error", err)
		} else {
			sse.PatchElements(buf.String(),
				datastar.WithSelector("#record-search-rows"),
				datastar.WithMode(datastar.ElementPatchModeAppend),
				datastar.WithUseViewTransitions(false),
			)
		}
	}

	// Render status update
	var statusBuf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&statusBuf, "record-search-status", map[string]any{
		"Searching": !update.done,
		"Email":     email,
		"Stopped":   false,
		"Progress":  update.progress,
	}); err != nil {
		slog.Error("template render failed", "template", "record-search-status", "error", err)
	} else {
		sse.PatchElements(statusBuf.String(),
			datastar.WithSelector("#record-search-status"),
			datastar.WithMode(datastar.ElementPatchModeOuter),
			datastar.WithUseViewTransitions(false),
		)
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

func (s *Server) getOrCreateSearchState(key string) *searchState {
	slog.Info("getOrCreateSearchState", "key", key)

	if key == "" {
		slog.Warn("empty search key, creating ephemeral state")
		return &searchState{
			seen:      make(map[string]struct{}),
			listeners: make(map[chan searchUpdate]struct{}),
		}
	}

	s.searchMu.Lock()
	defer s.searchMu.Unlock()

	if st, ok := s.searchStates[key]; ok {
		st.mu.Lock()
		slog.Info("reusing existing search state", "key", key, "done", st.done, "canceled", st.canceled, "results", len(st.results))
		st.lastActivity = time.Now()
		st.mu.Unlock()
		return st
	}

	slog.Info("creating new search state", "key", key)
	st := &searchState{
		seen:         make(map[string]struct{}),
		listeners:    make(map[chan searchUpdate]struct{}),
		lastActivity: time.Now(),
	}
	s.searchStates[key] = st
	return st
}

func (s *searchState) addResult(res RecordSearchResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastActivity = time.Now()
	key := res.ExecutionArn + "|" + strconv.Itoa(res.Index)
	if _, exists := s.seen[key]; exists {
		return false
	}

	s.seen[key] = struct{}{}
	s.results = append(s.results, res)
	return true
}

func (s *searchState) broadcastResult(result RecordSearchResult) {
	s.broadcast(searchUpdate{newResults: []RecordSearchResult{result}})
}

func (s *searchState) broadcastProgress(env, stateMachine string, checked, total int) {
	s.broadcast(searchUpdate{
		progress: &searchProgress{
			Env:          env,
			StateMachine: stateMachine,
			Checked:      checked,
			Total:        total,
		},
	})
}

func (s *searchState) broadcast(update searchUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.listeners {
		select {
		case ch <- update:
		default:
		}
	}
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
	defer s.mu.Unlock()

	if _, exists := s.listeners[ch]; !exists {
		return
	}

	delete(s.listeners, ch)
	close(ch)
}

func (s *searchState) isCanceled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canceled
}

func (s *searchState) cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceled = true
	s.running = false
	s.done = true
}

func (s *searchState) markDone() {
	s.mu.Lock()
	s.running = false
	s.done = true
	s.mu.Unlock()
	s.broadcast(searchUpdate{done: true})
}

func (s *Server) startSearchStateCleanup() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.cleanupOldSearchStates()
		}
	}()
}

func (s *Server) cleanupOldSearchStates() {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()

	now := time.Now()
	for key, state := range s.searchStates {
		state.mu.Lock()
		lastActive := state.lastActivity
		state.mu.Unlock()

		if now.Sub(lastActive) > s.searchStatesTTL {
			delete(s.searchStates, key)
		}
	}
}

func buildSearchKey(params searchParams) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(params.email)))
	b.WriteByte('|')
	b.WriteString(strings.ToLower(strings.TrimSpace(params.env)))
	b.WriteByte('|')
	b.WriteString(strings.ToLower(strings.TrimSpace(params.stateMachine)))
	b.WriteByte('|')
	if params.startAt != nil {
		b.WriteString(params.startAt.Format("2006-01-02"))
	}
	b.WriteByte('|')
	if params.endAt != nil {
		b.WriteString(params.endAt.Format("2006-01-02"))
	}
	return b.String()
}

func parseDateRange(startDate, endDate string) (*time.Time, *time.Time, error) {
	const layout = "2006-01-02"
	var startAt, endAt *time.Time

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
