package execution

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	app_notification "github.com/EliLillyCo/work-dashboard/internal/app/notification"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
	"github.com/EliLillyCo/work-dashboard/internal/infra/awsclient"
)

type ExecutionRepository interface {
	ListActive(ctx context.Context) ([]domain_execution.Summary, error)
	ListRecentFailed(ctx context.Context) ([]domain_execution.Summary, error)
	ListRecentCompleted(ctx context.Context, since time.Time) ([]domain_execution.Summary, error)
	ListStateMachines(ctx context.Context, ttl time.Duration) ([]domain_statemachine.StateMachine, error)
	ListExecutionsByStatus(ctx context.Context, stateMachineArn string, status domain_execution.Status, cutoff time.Time, maxPages int) ([]domain_execution.Ref, error)
	DescribeExecution(ctx context.Context, executionArn string) (*domain_execution.Detail, error)
	GetStateHistory(ctx context.Context, executionArn string) ([]domain_execution.State, error)
	ExtractLambdaARNsFromStateMachine(ctx context.Context, stateMachineArn string) ([]string, error)
}

type Service struct {
	repos  map[string]ExecutionRepository
	cfg    *config.Config
	notify *app_notification.Service

	activeCacheMu sync.Mutex
	activeCache   map[string][]domain_execution.Summary
	activeCacheAt map[string]time.Time

	activeCountMu sync.RWMutex
	activeCount   int

	activeIntervalMu     sync.Mutex
	activeIntervalLogged map[string]time.Duration

	credentialErrorMu  sync.RWMutex
	credentialError    bool
	credentialErrorMsg string
	credentialErrorAt  time.Time

	flashMu            sync.Mutex
	flashActiveSeen    map[string]time.Time
	flashCompletedSeen map[string]time.Time
	flashFailuresSeen  map[string]time.Time

	completedRenderMu   sync.Mutex
	completedRenderHash string
}

func NewService(repos map[string]ExecutionRepository, cfg *config.Config, notify *app_notification.Service) *Service {
	return &Service{
		repos:                repos,
		cfg:                  cfg,
		notify:               notify,
		activeCache:          make(map[string][]domain_execution.Summary),
		activeCacheAt:        make(map[string]time.Time),
		activeIntervalLogged: make(map[string]time.Duration),
		flashActiveSeen:      make(map[string]time.Time),
		flashCompletedSeen:   make(map[string]time.Time),
		flashFailuresSeen:    make(map[string]time.Time),
	}
}

func (s *Service) FetchActive() ([]domain_execution.Summary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var allActive []domain_execution.Summary
	var mu sync.Mutex
	var wg sync.WaitGroup

	for env, repo := range s.repos {
		wg.Add(1)
		go func(env string, repo ExecutionRepository) {
			defer wg.Done()

			interval := 30 * time.Second
			if s.cfg != nil && s.cfg.Polling.ActiveIntervalByEnv != nil {
				envKey := strings.ToLower(env)
				if v, ok := s.cfg.Polling.ActiveIntervalByEnv[envKey]; ok {
					interval = v
				} else if base := domain_statemachine.BaseEnvFromKey(envKey); base != "" {
					if v, ok := s.cfg.Polling.ActiveIntervalByEnv[base]; ok {
						interval = v
					}
				}
			}
			s.logActiveInterval(env, interval)

			now := time.Now()
			s.activeCacheMu.Lock()
			last := s.activeCacheAt[env]
			if !last.IsZero() && now.Sub(last) < interval {
				cached := markStaleExecutions(s.activeCache[env])
				s.activeCacheMu.Unlock()
				if len(cached) > 0 {
					mu.Lock()
					allActive = append(allActive, cached...)
					mu.Unlock()
				}
				return
			}
			s.activeCacheAt[env] = now
			s.activeCacheMu.Unlock()

			active, err := repo.ListActive(ctx)
			if err == nil {
				s.clearCredentialError()
				s.activeCacheMu.Lock()
				s.activeCache[env] = active
				s.activeCacheMu.Unlock()

				mu.Lock()
				allActive = append(allActive, active...)
				mu.Unlock()
				return
			}

			if awsclient.IsCredentialError(err) {
				s.setCredentialError(err)
			}

			slog.Warn("list active executions failed", "env", env, "err", err)
			s.activeCacheMu.Lock()
			cached := markStaleExecutions(s.activeCache[env])
			s.activeCacheMu.Unlock()
			if len(cached) > 0 {
				mu.Lock()
				allActive = append(allActive, cached...)
				mu.Unlock()
			}
		}(env, repo)
	}
	wg.Wait()

	sort.Slice(allActive, func(i, j int) bool {
		return allActive[i].StartTime.After(allActive[j].StartTime)
	})

	s.flashMu.Lock()
	allActive = s.markNewExecutions(allActive, s.flashActiveSeen)
	s.flashMu.Unlock()

	s.setActiveCount(len(allActive))
	if s.notify != nil {
		s.notify.ObserveActive(allActive)
	}

	return allActive, nil
}

func (s *Service) FetchRecentCompleted() ([]domain_execution.Summary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	since := time.Now().Add(-15 * time.Minute)
	var allCompleted []domain_execution.Summary
	var mu sync.Mutex
	var wg sync.WaitGroup

	for env, repo := range s.repos {
		wg.Add(1)
		go func(env string, repo ExecutionRepository) {
			defer wg.Done()

			execs, err := repo.ListRecentCompleted(ctx, since)
			if err != nil {
				slog.Warn("list recent completed failed", "env", env, "err", err)
				return
			}

			for i := range execs {
				if execs[i].ExecutionArn == "" {
					continue
				}
				detail, err := repo.DescribeExecution(ctx, execs[i].ExecutionArn)
				if err != nil || detail == nil {
					continue
				}
				execs[i].Input = detail.Summary.Input
				execs[i].Output = detail.Summary.Output
			}

			mu.Lock()
			allCompleted = append(allCompleted, execs...)
			mu.Unlock()
		}(env, repo)
	}
	wg.Wait()

	sort.Slice(allCompleted, func(i, j int) bool {
		return allCompleted[i].StopTime.After(allCompleted[j].StopTime)
	})

	s.flashMu.Lock()
	allCompleted = s.markNewExecutions(allCompleted, s.flashCompletedSeen)
	s.flashMu.Unlock()

	if len(allCompleted) > 50 {
		allCompleted = allCompleted[:50]
	}

	return allCompleted, nil
}

func (s *Service) FetchRecentFailures() ([]domain_execution.Summary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var allFailures []domain_execution.Summary
	var mu sync.Mutex
	var wg sync.WaitGroup

	for env, repo := range s.repos {
		wg.Add(1)
		go func(env string, repo ExecutionRepository) {
			defer wg.Done()
			failures, err := repo.ListRecentFailed(ctx)
			if err != nil {
				slog.Warn("list recent failures failed", "env", env, "err", err)
				return
			}
			mu.Lock()
			allFailures = append(allFailures, failures...)
			mu.Unlock()
		}(env, repo)
	}
	wg.Wait()

	sort.Slice(allFailures, func(i, j int) bool {
		return allFailures[i].StopTime.After(allFailures[j].StopTime)
	})

	s.flashMu.Lock()
	allFailures = s.markNewExecutions(allFailures, s.flashFailuresSeen)
	s.flashMu.Unlock()

	if s.notify != nil {
		s.notify.ObserveFailures(allFailures)
	}

	return allFailures, nil
}

func (s *Service) FetchStateMachines() ([]domain_statemachine.StateMachine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var items []domain_statemachine.StateMachine
	var mu sync.Mutex
	var wg sync.WaitGroup

	for env, repo := range s.repos {
		wg.Add(1)
		go func(env string, repo ExecutionRepository) {
			defer wg.Done()
			list, err := repo.ListStateMachines(ctx, 10*time.Minute)
			if err != nil {
				slog.Warn("list filtered state machines failed", "env", env, "err", err)
				return
			}
			mu.Lock()
			items = append(items, list...)
			mu.Unlock()
		}(env, repo)
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

func (s *Service) ListStateMachineExecutions(ctx context.Context, env, arn string, count int) ([]domain_execution.Detail, error) {
	repo := s.repos[env]
	if repo == nil {
		return nil, fmt.Errorf("unknown env")
	}

	maxPages := 1
	if count > 10 {
		maxPages = 2
	}
	if count > 50 {
		maxPages = 3
	}

	statuses := []domain_execution.Status{
		domain_execution.StatusRunning,
		domain_execution.StatusFailed,
		domain_execution.StatusSucceeded,
	}

	var refs []domain_execution.Ref
	var firstErr error
	for _, status := range statuses {
		items, err := repo.ListExecutionsByStatus(ctx, arn, status, time.Time{}, maxPages)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("list recent executions failed", "env", env, "arn", arn, "status", status, "err", err)
			continue
		}
		refs = append(refs, items...)
	}
	if len(refs) == 0 && firstErr != nil {
		return nil, firstErr
	}

	sort.Slice(refs, func(i, j int) bool {
		return refs[i].StartTime.After(refs[j].StartTime)
	})
	if len(refs) > count {
		refs = refs[:count]
	}

	details := make([]domain_execution.Detail, 0, len(refs))
	for _, ref := range refs {
		detail := &domain_execution.Detail{
			Summary: domain_execution.Summary{
				Env:           env,
				Name:          ref.Name,
				ExecutionName: ref.Name,
				ExecutionArn:  ref.ExecutionArn,
				Status:        ref.Status,
				StartTime:     ref.StartTime,
				StopTime:      ref.StopTime,
			},
		}

		enriched, err := repo.DescribeExecution(ctx, ref.ExecutionArn)
		if err == nil && enriched != nil {
			detail = enriched
		} else if err != nil {
			slog.Warn("describe execution failed during state machine listing", "env", env, "execution_arn", ref.ExecutionArn, "err", err)
		}
		detail.Summary.Env = env
		detail.Summary.Name = ref.Name
		detail.Summary.ExecutionName = ref.Name
		detail.Summary.ExecutionArn = ref.ExecutionArn
		if detail.Summary.Status == "" {
			detail.Summary.Status = ref.Status
		}
		if detail.Summary.StartTime.IsZero() {
			detail.Summary.StartTime = ref.StartTime
		}
		if detail.Summary.StopTime.IsZero() {
			detail.Summary.StopTime = ref.StopTime
		}
		details = append(details, *detail)
	}

	return details, nil
}

func (s *Service) GetExecutionStates(ctx context.Context, env, executionArn string) (string, []domain_execution.State, error) {
	repo := s.repos[env]
	if repo == nil {
		return "", nil, fmt.Errorf("unknown env")
	}

	detail, err := repo.DescribeExecution(ctx, executionArn)
	if err != nil {
		return "", nil, err
	}
	states, err := repo.GetStateHistory(ctx, executionArn)
	if err != nil {
		return "", nil, err
	}
	if detail == nil {
		return "", states, nil
	}
	return detail.StateMachine, states, nil
}

func (s *Service) ActiveCount() int {
	s.activeCountMu.RLock()
	defer s.activeCountMu.RUnlock()
	return s.activeCount
}

func (s *Service) CredentialError() (bool, string, time.Time) {
	s.credentialErrorMu.RLock()
	defer s.credentialErrorMu.RUnlock()
	return s.credentialError, s.credentialErrorMsg, s.credentialErrorAt
}

func (s *Service) IsSameCompletedSnapshot(execs []domain_execution.Summary) bool {
	h := sha1.New()
	for _, exec := range execs {
		h.Write([]byte(exec.ExecutionArn))
		h.Write([]byte(exec.Status))
		if !exec.StopTime.IsZero() {
			h.Write([]byte(exec.StopTime.UTC().Format(time.RFC3339Nano)))
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

func markStaleExecutions(src []domain_execution.Summary) []domain_execution.Summary {
	if len(src) == 0 {
		return nil
	}
	out := make([]domain_execution.Summary, len(src))
	copy(out, src)
	for i := range out {
		out[i].Stale = true
	}
	return out
}

func (s *Service) markNewExecutions(execs []domain_execution.Summary, seen map[string]time.Time) []domain_execution.Summary {
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

func (s *Service) setCredentialError(err error) {
	s.credentialErrorMu.Lock()
	defer s.credentialErrorMu.Unlock()
	s.credentialError = true
	s.credentialErrorMsg = err.Error()
	s.credentialErrorAt = time.Now()
}

func (s *Service) clearCredentialError() {
	s.credentialErrorMu.Lock()
	defer s.credentialErrorMu.Unlock()
	s.credentialError = false
	s.credentialErrorMsg = ""
}

func (s *Service) setActiveCount(count int) {
	s.activeCountMu.Lock()
	defer s.activeCountMu.Unlock()
	s.activeCount = count
}

func (s *Service) logActiveInterval(env string, interval time.Duration) {
	s.activeIntervalMu.Lock()
	defer s.activeIntervalMu.Unlock()
	last, ok := s.activeIntervalLogged[env]
	if ok && last == interval {
		return
	}
	s.activeIntervalLogged[env] = interval
	slog.Info("active polling interval", "env", env, "interval", interval)
}
