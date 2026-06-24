package lambda

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	domain_lambda "github.com/EliLillyCo/work-dashboard/internal/domain/lambda"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

type ExecutionRepository interface {
	ListStateMachines(ctx context.Context, ttl time.Duration) ([]domain_statemachine.StateMachine, error)
	ExtractLambdaARNsFromStateMachine(ctx context.Context, stateMachineArn string) ([]string, error)
}

type LambdaRepository interface {
	GetMetrics(ctx context.Context, functionName string, usedBy []string) (*domain_lambda.LambdaMetrics, error)
}

type LambdaFunction struct {
	Env             string
	FunctionName    string
	FunctionArn     string
	UsedByStepFuncs []string
}

type LambdaRegistry struct {
	lambdaFunctions map[string]*LambdaFunction
	lastDiscovery   time.Time
}

type Report struct {
	Metrics      []*domain_lambda.LambdaMetrics
	WarningCount int
	LastUpdated  time.Time
}

type cachedMetrics struct {
	Metrics   *domain_lambda.LambdaMetrics
	FetchedAt time.Time
}

type Service struct {
	execRepos   map[string]ExecutionRepository
	lambdaRepos map[string]LambdaRepository
	activeCount func() int

	registryMu sync.RWMutex
	registry   *LambdaRegistry

	cacheMu sync.Mutex
	cache   map[string]cachedMetrics
}

func NewService(execRepos map[string]ExecutionRepository, lambdaRepos map[string]LambdaRepository, activeCount func() int) *Service {
	return &Service{
		execRepos:   execRepos,
		lambdaRepos: lambdaRepos,
		activeCount: activeCount,
		cache:       make(map[string]cachedMetrics),
	}
}

func (s *Service) FetchMetrics() (*Report, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := s.discover(ctx); err != nil {
		slog.Warn("failed to discover lambda functions", "error", err)
	}

	interval := 5 * time.Minute
	if s.activeCount != nil && s.activeCount() > 0 {
		interval = time.Minute
	}

	var allMetrics []*domain_lambda.LambdaMetrics
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	s.registryMu.RLock()
	if s.registry == nil || len(s.registry.lambdaFunctions) == 0 {
		s.registryMu.RUnlock()
		return &Report{Metrics: []*domain_lambda.LambdaMetrics{}, LastUpdated: time.Now()}, nil
	}

	for key, lambdaFunc := range s.registry.lambdaFunctions {
		now := time.Now()
		s.cacheMu.Lock()
		if cached, ok := s.cache[key]; ok && now.Sub(cached.FetchedAt) < interval {
			allMetrics = append(allMetrics, cached.Metrics)
			s.cacheMu.Unlock()
			continue
		}
		s.cacheMu.Unlock()

		repo := s.lambdaRepos[lambdaFunc.Env]
		if repo == nil {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(repo LambdaRepository, lf *LambdaFunction, cacheKey string, now time.Time) {
			defer wg.Done()
			defer func() { <-sem }()

			metrics, err := repo.GetMetrics(ctx, lf.FunctionName, lf.UsedByStepFuncs)
			if err != nil || metrics == nil {
				if err != nil {
					slog.Debug("failed to get lambda metrics", "env", lf.Env, "function", lf.FunctionName, "error", err)
				}
				return
			}

			s.cacheMu.Lock()
			s.cache[cacheKey] = cachedMetrics{Metrics: metrics, FetchedAt: now}
			s.cacheMu.Unlock()

			mu.Lock()
			allMetrics = append(allMetrics, metrics)
			mu.Unlock()
		}(repo, lambdaFunc, key, now)
	}
	s.registryMu.RUnlock()

	wg.Wait()

	sort.Slice(allMetrics, func(i, j int) bool {
		return allMetrics[i].TimeoutPercent > allMetrics[j].TimeoutPercent
	})

	warnings := 0
	for _, metric := range allMetrics {
		if metric.TimeoutPercent >= domain_lambda.TimeoutWarningHigh {
			warnings++
		}
	}

	return &Report{
		Metrics:      allMetrics,
		WarningCount: warnings,
		LastUpdated:  time.Now(),
	}, nil
}

func (s *Service) CachedReport() *Report {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if len(s.cache) == 0 {
		return &Report{Metrics: []*domain_lambda.LambdaMetrics{}, LastUpdated: time.Now()}
	}

	allMetrics := make([]*domain_lambda.LambdaMetrics, 0, len(s.cache))
	for _, cached := range s.cache {
		allMetrics = append(allMetrics, cached.Metrics)
	}
	sort.Slice(allMetrics, func(i, j int) bool {
		return allMetrics[i].TimeoutPercent > allMetrics[j].TimeoutPercent
	})
	warnings := 0
	for _, metric := range allMetrics {
		if metric.TimeoutPercent >= domain_lambda.TimeoutWarningHigh {
			warnings++
		}
	}
	return &Report{Metrics: allMetrics, WarningCount: warnings, LastUpdated: time.Now()}
}

func (s *Service) discover(ctx context.Context) error {
	now := time.Now()

	s.registryMu.Lock()
	if s.registry != nil && now.Sub(s.registry.lastDiscovery) < 15*time.Minute {
		s.registryMu.Unlock()
		return nil
	}
	s.registryMu.Unlock()

	registry := &LambdaRegistry{
		lambdaFunctions: make(map[string]*LambdaFunction),
		lastDiscovery:   now,
	}

	for env, repo := range s.execRepos {
		stateMachines, err := repo.ListStateMachines(ctx, time.Hour)
		if err != nil {
			slog.Warn("failed to list state machines for lambda discovery", "env", env, "error", err)
			continue
		}

		for _, sm := range stateMachines {
			lambdaARNs, err := repo.ExtractLambdaARNsFromStateMachine(ctx, sm.Arn)
			if err != nil {
				slog.Debug("failed to extract lambdas from state machine", "env", env, "sm", sm.Name, "error", err)
				continue
			}

			for _, arn := range lambdaARNs {
				functionName := domain_lambda.GetFunctionNameFromArn(arn)
				if functionName == "" {
					functionName = arn
				}
				key := fmt.Sprintf("%s:%s", env, functionName)
				if existing, ok := registry.lambdaFunctions[key]; ok {
					existing.UsedByStepFuncs = append(existing.UsedByStepFuncs, sm.Name)
					continue
				}
				registry.lambdaFunctions[key] = &LambdaFunction{
					Env:             env,
					FunctionName:    functionName,
					FunctionArn:     arn,
					UsedByStepFuncs: []string{sm.Name},
				}
			}
		}
	}

	s.registryMu.Lock()
	s.registry = registry
	s.registryMu.Unlock()

	return nil
}
