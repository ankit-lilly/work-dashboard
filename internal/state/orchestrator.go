package state

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	app_notification "github.com/EliLillyCo/work-dashboard/internal/app/notification"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

// ActiveFetcher fetches active executions from all environments.
type ActiveFetcher func() ([]domain_execution.Summary, error)

// CompletedFetcher fetches recently completed executions.
type CompletedFetcher func() ([]domain_execution.Summary, error)

// FailuresFetcher fetches recent failure executions.
type FailuresFetcher func() ([]domain_execution.Summary, error)

// StateMachinesFetcher fetches state machine definitions.
type StateMachinesFetcher func() ([]domain_statemachine.StateMachine, error)

// RDSFetcher fetches RDS performance metrics.
type RDSFetcher func() ([]domain_rds.RDSMetric, error)

// LambdaFetcher fetches Lambda metrics.
type LambdaFetcher func() (*app_lambda.Report, error)

// CredentialChecker returns current credential error state.
type CredentialChecker func() (hasError bool, msg string, at time.Time)

// OrchestratorConfig holds the fetch functions and polling intervals.
type OrchestratorConfig struct {
	FetchActive        ActiveFetcher
	FetchCompleted     CompletedFetcher
	FetchFailures      FailuresFetcher
	FetchStateMachines StateMachinesFetcher
	FetchRDS           RDSFetcher
	FetchLambda        LambdaFetcher
	CheckCredentials   CredentialChecker
	Notify             *app_notification.Service

	Polling *config.PollingConfig
}

// schedule tracks when each section was last fetched and its interval.
type schedule struct {
	interval    time.Duration
	lastFetched time.Time
}

// Orchestrator is the single writer goroutine that fetches data and updates DashboardState.
type Orchestrator struct {
	state  *DashboardState
	cfg    OrchestratorConfig
	stopCh chan struct{}

	schedules map[Section]*schedule
}

// NewOrchestrator creates the orchestrator wired to the given state and fetchers.
func NewOrchestrator(state *DashboardState, cfg OrchestratorConfig) *Orchestrator {
	polling := cfg.Polling
	if polling == nil {
		polling = &config.PollingConfig{
			ActiveInterval:        5 * time.Second,
			FailuresInterval:      120 * time.Second,
			StateMachinesInterval: 5 * time.Minute,
			RDSFastInterval:       30 * time.Second,
			RDSSlowInterval:       30 * time.Minute,
		}
	}

	o := &Orchestrator{
		state:  state,
		cfg:    cfg,
		stopCh: make(chan struct{}),
		schedules: map[Section]*schedule{
			SectionActive:        {interval: polling.ActiveInterval},
			SectionCompleted:     {interval: polling.ActiveInterval}, // same as active
			SectionFailures:      {interval: polling.ActiveInterval}, // same as active
			SectionRDS:           {interval: polling.RDSFastInterval},
			SectionLambda:        {interval: 60 * time.Second},
			SectionStateMachines: {interval: polling.StateMachinesInterval},
		},
	}
	return o
}

// Start begins the orchestrator's tick loop. Call Stop() to shut it down.
func (o *Orchestrator) Start() {
	go o.run()
}

// Stop signals the orchestrator to shut down.
func (o *Orchestrator) Stop() {
	close(o.stopCh)
}

func (o *Orchestrator) run() {
	slog.Info("state orchestrator started")

	// Fetch everything immediately on startup.
	o.tick(true)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-o.stopCh:
			slog.Info("state orchestrator stopped")
			return
		case <-ticker.C:
			o.tick(false)
		}
	}
}

func (o *Orchestrator) tick(forceAll bool) {
	// Adapt RDS interval based on active execution count.
	o.adaptIntervals()

	now := time.Now()
	due := make(map[Section]bool)

	for section, sched := range o.schedules {
		if forceAll || now.Sub(sched.lastFetched) >= sched.interval {
			due[section] = true
		}
	}

	if len(due) == 0 {
		return
	}

	// Fetch all due sections concurrently.
	type fetchResult struct {
		section Section
		data    any
	}

	var wg sync.WaitGroup
	results := make(chan fetchResult, len(due))

	if due[SectionActive] && o.cfg.FetchActive != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := o.cfg.FetchActive()
			if err != nil {
				slog.Warn("orchestrator: fetch active failed", "err", err)
				return
			}
			results <- fetchResult{SectionActive, data}
		}()
	}

	if due[SectionCompleted] && o.cfg.FetchCompleted != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := o.cfg.FetchCompleted()
			if err != nil {
				slog.Warn("orchestrator: fetch completed failed", "err", err)
				return
			}
			results <- fetchResult{SectionCompleted, data}
		}()
	}

	if due[SectionFailures] && o.cfg.FetchFailures != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := o.cfg.FetchFailures()
			if err != nil {
				slog.Warn("orchestrator: fetch failures failed", "err", err)
				return
			}
			results <- fetchResult{SectionFailures, data}
		}()
	}

	if due[SectionStateMachines] && o.cfg.FetchStateMachines != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := o.cfg.FetchStateMachines()
			if err != nil {
				slog.Warn("orchestrator: fetch state machines failed", "err", err)
				return
			}
			results <- fetchResult{SectionStateMachines, data}
		}()
	}

	if due[SectionRDS] && o.cfg.FetchRDS != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := o.cfg.FetchRDS()
			if err != nil {
				slog.Warn("orchestrator: fetch RDS failed", "err", err)
				return
			}
			results <- fetchResult{SectionRDS, data}
		}()
	}

	if due[SectionLambda] && o.cfg.FetchLambda != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := o.cfg.FetchLambda()
			if err != nil {
				slog.Warn("orchestrator: fetch lambda failed", "err", err)
				return
			}
			results <- fetchResult{SectionLambda, data}
		}()
	}

	// Wait for all fetches, then collect results.
	go func() {
		wg.Wait()
		close(results)
	}()

	fetched := make(map[Section]any)
	for r := range results {
		fetched[r.section] = r.data
	}

	if len(fetched) == 0 {
		return
	}

	// Mark fetch times for successfully fetched sections.
	for section := range fetched {
		o.schedules[section].lastFetched = now
	}

	// Apply all fetched data atomically to state.
	fetchDuration := time.Since(now)
	o.applyUpdate(fetched, fetchDuration)
}

func (o *Orchestrator) applyUpdate(fetched map[Section]any, fetchDuration time.Duration) {
	ds := o.state
	ds.mu.Lock()
	defer ds.mu.Unlock()

	var changed []Section

	if data, ok := fetched[SectionActive]; ok {
		active := data.([]domain_execution.Summary)
		// Apply flash marking
		active = ds.MarkNewExecutions(active, ds.flashActiveSeen)
		// Sort by start time descending
		sort.Slice(active, func(i, j int) bool {
			return active[i].StartTime.After(active[j].StartTime)
		})
		// Check if content actually changed
		newHash := hashExecutions(active)
		if newHash != ds.hashes[SectionActive] {
			ds.active = active
			ds.activeCount = len(active)
			ds.hashes[SectionActive] = newHash
			changed = append(changed, SectionActive)
		}
		// Notify for new active executions
		if o.cfg.Notify != nil {
			o.cfg.Notify.ObserveActive(active)
		}
	}

	if data, ok := fetched[SectionCompleted]; ok {
		completed := data.([]domain_execution.Summary)
		// Apply flash marking
		completed = ds.MarkNewExecutions(completed, ds.flashCompletedSeen)
		// Sort by stop time descending
		sort.Slice(completed, func(i, j int) bool {
			return completed[i].StopTime.After(completed[j].StopTime)
		})
		if len(completed) > 50 {
			completed = completed[:50]
		}
		newHash := hashExecutions(completed)
		if newHash != ds.hashes[SectionCompleted] {
			ds.completed = completed
			ds.hashes[SectionCompleted] = newHash
			changed = append(changed, SectionCompleted)
		}
	}

	if data, ok := fetched[SectionFailures]; ok {
		failures := data.([]domain_execution.Summary)
		// Apply flash marking
		failures = ds.MarkNewExecutions(failures, ds.flashFailuresSeen)
		// Sort by stop time descending
		sort.Slice(failures, func(i, j int) bool {
			return failures[i].StopTime.After(failures[j].StopTime)
		})
		newHash := hashExecutions(failures)
		if newHash != ds.hashes[SectionFailures] {
			ds.failures = failures
			ds.hashes[SectionFailures] = newHash
			changed = append(changed, SectionFailures)
		}
		// Notify for new failures
		if o.cfg.Notify != nil {
			o.cfg.Notify.ObserveFailures(failures)
		}
	}

	if data, ok := fetched[SectionStateMachines]; ok {
		sms := data.([]domain_statemachine.StateMachine)
		newHash := hashStateMachines(sms)
		if newHash != ds.hashes[SectionStateMachines] {
			ds.stateMachines = sms
			ds.hashes[SectionStateMachines] = newHash
			changed = append(changed, SectionStateMachines)
		}
	}

	if data, ok := fetched[SectionRDS]; ok {
		metrics := data.([]domain_rds.RDSMetric)
		newHash := hashRDSMetrics(metrics)
		if newHash != ds.hashes[SectionRDS] {
			ds.rdsMetrics = metrics
			ds.hashes[SectionRDS] = newHash
			changed = append(changed, SectionRDS)
		}
	}

	if data, ok := fetched[SectionLambda]; ok {
		report := data.(*app_lambda.Report)
		newHash := hashLambdaReport(report)
		if newHash != ds.hashes[SectionLambda] {
			ds.lambdaReport = report
			ds.hashes[SectionLambda] = newHash
			changed = append(changed, SectionLambda)
		}
	}

	// Check credential errors
	if o.cfg.CheckCredentials != nil {
		hasErr, msg, _ := o.cfg.CheckCredentials()
		if hasErr != ds.credentialError || msg != ds.credentialErrorMsg {
			ds.credentialError = hasErr
			ds.credentialErrorMsg = msg
			// Credential change is always pushed (via any changed section)
			if len(changed) == 0 {
				// Force an active section push to communicate the credential change
				changed = append(changed, SectionActive)
			}
		}
	}

	if len(changed) == 0 {
		return
	}

	// Bump version and notify.
	ds.version++

	snap := Snapshot{
		Version:            ds.version,
		Changed:            changed,
		Active:             ds.active,
		Completed:          ds.completed,
		Failures:           ds.failures,
		StateMachines:      ds.stateMachines,
		RDSMetrics:         ds.rdsMetrics,
		LambdaReport:       ds.lambdaReport,
		CredentialError:    ds.credentialError,
		CredentialErrorMsg: ds.credentialErrorMsg,
		ActiveCount:        ds.activeCount,
	}

	slog.Info("state updated", "version", ds.version, "changed", changed, "fetchDuration", fetchDuration)

	// Unlock before notifying to avoid holding the state lock during channel sends.
	ds.mu.Unlock()
	ds.notify(snap)
	ds.mu.Lock() // re-acquire for the deferred unlock
}

// adaptIntervals dynamically adjusts polling intervals based on current state.
func (o *Orchestrator) adaptIntervals() {
	ds := o.state
	ds.mu.RLock()
	count := ds.activeCount
	ds.mu.RUnlock()

	polling := o.cfg.Polling
	if polling == nil {
		return
	}

	// RDS polls fast when there are active executions, slow when idle.
	rdsSched := o.schedules[SectionRDS]
	if count > 0 {
		rdsSched.interval = polling.RDSFastInterval
	} else {
		rdsSched.interval = polling.RDSSlowInterval
	}
}
