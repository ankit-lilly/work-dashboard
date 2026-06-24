package search

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/config"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_search "github.com/EliLillyCo/work-dashboard/internal/domain/search"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

type ExecutionSearchRepository interface {
	ListStateMachines(ctx context.Context, ttl time.Duration) ([]domain_statemachine.StateMachine, error)
	ListExecutionsByStatus(ctx context.Context, stateMachineArn string, status domain_execution.Status, cutoff time.Time, maxPages int) ([]domain_execution.Ref, error)
	DescribeExecution(ctx context.Context, executionArn string) (*domain_execution.Detail, error)
}

type ObjectSearchRepository interface {
	HeadObjectSize(ctx context.Context, bucket, key string) (int64, error)
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	ListObjects(ctx context.Context, bucket, prefix string, maxKeys int32) ([]domain_search.ObjectInfo, error)
	FindEmail(ctx context.Context, bucket, key, email string) (*domain_search.RecordMatch, error)
	FindString(ctx context.Context, bucket, key, query string, limit int) ([]domain_search.StringMatch, error)
}

type Query struct {
	Email        string
	Env          string
	StateMachine string
	StartAt      *time.Time
	EndAt        *time.Time
}

type Result struct {
	Env           string
	StateMachine  string
	ExecutionName string
	ExecutionArn  string
	StartTime     time.Time
	Bucket        string
	Key           string
	Index         int
	Record        string
	OutputBucket  string
	OutputPrefix  string
	OutputKey     string
}

type Progress struct {
	Env          string
	StateMachine string
	Checked      int
	Total        int
}

type Update struct {
	NewResults []Result
	Done       bool
	Progress   *Progress
}

type Snapshot struct {
	Key       string
	Results   []Result
	Searching bool
	Stopped   bool
}

type searchState struct {
	mu           sync.Mutex
	running      bool
	done         bool
	canceled     bool
	results      []Result
	seen         map[string]struct{}
	listeners    map[chan Update]struct{}
	lastActivity time.Time
}

type Service struct {
	execRepos map[string]ExecutionSearchRepository
	objRepos  map[string]ObjectSearchRepository
	ttl       time.Duration

	searchMu     sync.Mutex
	searchStates map[string]*searchState
}

func NewService(execRepos map[string]ExecutionSearchRepository, objRepos map[string]ObjectSearchRepository, cfg *config.Config) *Service {
	ttl := 10 * time.Minute
	if cfg != nil && cfg.Limits.SearchStateTTL > 0 {
		ttl = cfg.Limits.SearchStateTTL
	}
	svc := &Service{
		execRepos:    execRepos,
		objRepos:     objRepos,
		ttl:          ttl,
		searchStates: make(map[string]*searchState),
	}
	svc.startCleanup()
	return svc
}

func (s *Service) EnsureSearch(ctx context.Context, query Query) Snapshot {
	key := s.BuildKey(query)
	state := s.getOrCreateSearchState(key)
	s.startSearchIfNeeded(ctx, query, state)

	state.mu.Lock()
	defer state.mu.Unlock()

	results := make([]Result, len(state.results))
	copy(results, state.results)
	return Snapshot{
		Key:       key,
		Results:   results,
		Searching: query.Email != "" && !state.done && !state.canceled,
		Stopped:   state.canceled,
	}
}

func (s *Service) Subscribe(key string) chan Update {
	return s.getOrCreateSearchState(key).subscribe()
}

func (s *Service) Unsubscribe(key string, ch chan Update) {
	s.getOrCreateSearchState(key).unsubscribe(ch)
}

func (s *Service) Cancel(key string) {
	s.getOrCreateSearchState(key).cancel()
}

func (s *Service) BuildKey(query Query) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(query.Email)))
	b.WriteByte('|')
	b.WriteString(strings.ToLower(strings.TrimSpace(query.Env)))
	b.WriteByte('|')
	b.WriteString(strings.ToLower(strings.TrimSpace(query.StateMachine)))
	b.WriteByte('|')
	if query.StartAt != nil {
		b.WriteString(query.StartAt.Format("2006-01-02"))
	}
	b.WriteByte('|')
	if query.EndAt != nil {
		b.WriteString(query.EndAt.Format("2006-01-02"))
	}
	return b.String()
}

func (s *Service) ParseDateRange(startDate, endDate string) (*time.Time, *time.Time, error) {
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

func (s *Service) FindString(ctx context.Context, env, bucket, key, query string, limit int) ([]domain_search.StringMatch, error) {
	repo := s.objRepos[env]
	if repo == nil {
		return nil, fmt.Errorf("unknown env")
	}
	return repo.FindString(ctx, bucket, key, query, limit)
}

func (s *Service) PreviewObject(ctx context.Context, env, bucket, key string, maxSize int64) ([]byte, error) {
	repo := s.objRepos[env]
	if repo == nil {
		return nil, fmt.Errorf("unknown env")
	}
	size, err := repo.HeadObjectSize(ctx, bucket, key)
	if err == nil && maxSize > 0 && size > maxSize {
		return nil, fmt.Errorf("File too large to preview. Please download.")
	}
	body, err := repo.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func (s *Service) DownloadObject(ctx context.Context, env, bucket, key string) (io.ReadCloser, error) {
	repo := s.objRepos[env]
	if repo == nil {
		return nil, fmt.Errorf("unknown env")
	}
	return repo.GetObject(ctx, bucket, key)
}

func (s *Service) ListPrefixObjects(ctx context.Context, env, bucket, prefix string) ([]domain_search.ObjectInfo, error) {
	repo := s.objRepos[env]
	if repo == nil {
		return nil, fmt.Errorf("unknown env")
	}
	items, err := repo.ListObjects(ctx, bucket, prefix, 50)
	if err != nil {
		return nil, err
	}

	filtered := make([]domain_search.ObjectInfo, 0, len(items))
	for _, item := range items {
		base := path.Base(item.Key)
		if base != "manifest.json" && base != "SUCCEEDED_0.json" && !(strings.HasPrefix(base, "ERROR") && strings.HasSuffix(base, ".json")) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}

func (s *Service) startSearchIfNeeded(ctx context.Context, query Query, state *searchState) {
	state.mu.Lock()
	shouldStart := query.Email != "" && !state.running && !state.done && !state.canceled
	if shouldStart {
		state.running = true
	}
	state.mu.Unlock()

	if shouldStart {
		go s.searchAllEnvironments(ctx, query, state)
	}
}

func (s *Service) searchAllEnvironments(ctx context.Context, query Query, state *searchState) {
	defer state.markDone()

	repos := s.matchingRepos(query.Env)
	if len(repos) == 0 {
		return
	}

	var wg sync.WaitGroup
	for env, repo := range repos {
		wg.Add(1)
		go func(env string, repo ExecutionSearchRepository) {
			defer wg.Done()
			s.searchEnvironment(ctx, env, repo, query, state)
		}(env, repo)
	}
	wg.Wait()
}

func (s *Service) searchEnvironment(ctx context.Context, env string, repo ExecutionSearchRepository, query Query, state *searchState) {
	stateMachines, err := repo.ListStateMachines(ctx, 10*time.Minute)
	if err != nil {
		slog.Warn("list state machines failed", "env", env, "err", err)
		return
	}

	filtered := make([]domain_statemachine.StateMachine, 0, len(stateMachines))
	for _, sm := range stateMachines {
		if domain_statemachine.IsRulesProcessor(sm.Name) && matchesFilter(sm, query.StateMachine) {
			filtered = append(filtered, sm)
		}
	}

	var wg sync.WaitGroup
	for idx, sm := range filtered {
		if state.isCanceled() || ctx.Err() != nil {
			return
		}
		wg.Add(1)
		go func(sm domain_statemachine.StateMachine, index int) {
			defer wg.Done()
			s.searchStateMachine(ctx, env, repo, sm, query, state, index, len(filtered))
		}(sm, idx)
	}
	wg.Wait()
}

func (s *Service) searchStateMachine(ctx context.Context, env string, repo ExecutionSearchRepository, sm domain_statemachine.StateMachine, query Query, state *searchState, index, total int) {
	if state.isCanceled() || ctx.Err() != nil {
		return
	}
	state.broadcastProgress(env, sm.Name, index, total)

	executions := s.listAllExecutions(ctx, repo, sm.Arn, query.StartAt)
	s.searchExecutionsForEmail(ctx, env, repo, executions, sm.Name, query, state)
}

func (s *Service) searchExecutionsForEmail(ctx context.Context, env string, repo ExecutionSearchRepository, executions []domain_execution.Ref, smName string, query Query, state *searchState) {
	for _, exec := range executions {
		if state.isCanceled() || ctx.Err() != nil {
			return
		}
		if !executionInDateRange(exec, query.StartAt, query.EndAt) {
			continue
		}
		result := s.checkExecutionForEmail(ctx, env, repo, exec, smName, query.Email)
		if result != nil && state.addResult(*result) {
			state.broadcastResult(*result)
		}
	}
}

func (s *Service) checkExecutionForEmail(ctx context.Context, env string, repo ExecutionSearchRepository, exec domain_execution.Ref, smName, email string) *Result {
	detail, err := repo.DescribeExecution(ctx, exec.ExecutionArn)
	if err != nil || detail == nil {
		return nil
	}

	bucket := detail.Summary.Input.Bucket
	key := detail.Summary.Input.Key
	if bucket == "" || key == "" {
		return nil
	}

	objRepo := s.objRepos[env]
	if objRepo == nil {
		return nil
	}
	match, err := objRepo.FindEmail(ctx, bucket, key, email)
	if err != nil || match == nil {
		if err != nil {
			slog.Warn("find email error", "env", env, "exec", exec.Name, "bucket", bucket, "key", key, "err", err)
		}
		return nil
	}

	return &Result{
		Env:           env,
		StateMachine:  smName,
		ExecutionName: exec.Name,
		ExecutionArn:  exec.ExecutionArn,
		StartTime:     exec.StartTime,
		Bucket:        bucket,
		Key:           key,
		Index:         match.Index,
		Record:        match.Record,
		OutputBucket:  detail.Summary.Output.Bucket,
		OutputPrefix:  detail.Summary.Output.Prefix,
		OutputKey:     detail.Summary.Output.Key,
	}
}

func (s *Service) listAllExecutions(ctx context.Context, repo ExecutionSearchRepository, arn string, startAt *time.Time) []domain_execution.Ref {
	cutoff := time.Time{}
	if startAt != nil {
		cutoff = *startAt
	}

	statuses := []domain_execution.Status{
		domain_execution.StatusRunning,
		domain_execution.StatusFailed,
		domain_execution.StatusSucceeded,
	}

	var results []domain_execution.Ref
	for _, status := range statuses {
		execs, err := repo.ListExecutionsByStatus(ctx, arn, status, cutoff, 50)
		if err != nil {
			slog.Warn("list executions failed", "arn", arn, "err", err)
			return nil
		}
		results = append(results, execs...)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].StartTime.After(results[j].StartTime)
	})
	return results
}

func (s *Service) matchingRepos(envFilter string) map[string]ExecutionSearchRepository {
	out := make(map[string]ExecutionSearchRepository)
	for env, repo := range s.execRepos {
		if domain_statemachine.EnvMatchesSelection(env, envFilter) {
			out[env] = repo
		}
	}
	return out
}

func (s *Service) getOrCreateSearchState(key string) *searchState {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()

	if st, ok := s.searchStates[key]; ok {
		st.mu.Lock()
		st.lastActivity = time.Now()
		st.mu.Unlock()
		return st
	}

	st := &searchState{
		seen:         make(map[string]struct{}),
		listeners:    make(map[chan Update]struct{}),
		lastActivity: time.Now(),
	}
	s.searchStates[key] = st
	return st
}

func (s *Service) startCleanup() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.cleanupOldSearchStates()
		}
	}()
}

func (s *Service) cleanupOldSearchStates() {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()

	now := time.Now()
	for key, state := range s.searchStates {
		state.mu.Lock()
		lastActive := state.lastActivity
		state.mu.Unlock()
		if now.Sub(lastActive) > s.ttl {
			delete(s.searchStates, key)
		}
	}
}

func (s *searchState) addResult(res Result) bool {
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

func (s *searchState) broadcastResult(result Result) {
	s.broadcast(Update{NewResults: []Result{result}})
}

func (s *searchState) broadcastProgress(env, stateMachine string, checked, total int) {
	s.broadcast(Update{
		Progress: &Progress{
			Env:          env,
			StateMachine: stateMachine,
			Checked:      checked,
			Total:        total,
		},
	})
}

func (s *searchState) broadcast(update Update) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.listeners {
		select {
		case ch <- update:
		default:
		}
	}
}

func (s *searchState) subscribe() chan Update {
	ch := make(chan Update, 4)
	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *searchState) unsubscribe(ch chan Update) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.listeners[ch]; !exists {
		return
	}
	delete(s.listeners, ch)
	close(ch)
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
	s.broadcast(Update{Done: true})
}

func (s *searchState) isCanceled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.canceled
}

func executionInDateRange(exec domain_execution.Ref, startAt, endAt *time.Time) bool {
	if exec.StartTime.IsZero() {
		return false
	}
	if startAt != nil && exec.StartTime.Before(*startAt) {
		return false
	}
	if endAt != nil && exec.StartTime.After(*endAt) {
		return false
	}
	return true
}

func matchesFilter(sm domain_statemachine.StateMachine, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	name := strings.ToLower(sm.Name)
	arn := strings.ToLower(sm.Arn)
	return strings.Contains(name, filter) || strings.Contains(arn, filter)
}
