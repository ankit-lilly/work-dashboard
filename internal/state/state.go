package state

import (
	"sync"
	"time"

	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

// Section identifies a section of the dashboard state that can change independently.
type Section string

const (
	SectionActive        Section = "active"
	SectionCompleted     Section = "completed"
	SectionFailures      Section = "failures"
	SectionRDS           Section = "rds"
	SectionLambda        Section = "lambda"
	SectionStateMachines Section = "state_machines"
)

// Snapshot is an immutable view of all dashboard state at a particular version.
type Snapshot struct {
	Version uint64

	Active        []domain_execution.Summary
	Completed     []domain_execution.Summary
	Failures      []domain_execution.Summary
	StateMachines []domain_statemachine.StateMachine
	RDSMetrics    []domain_rds.RDSMetric
	LambdaReport  *app_lambda.Report

	CredentialError    bool
	CredentialErrorMsg string
	ActiveCount        int
}

// DashboardState is the single source of truth for all dashboard data.
// One goroutine (the orchestrator) writes to it; SSE handlers read from it.
type DashboardState struct {
	mu      sync.RWMutex
	version uint64

	active        []domain_execution.Summary
	completed     []domain_execution.Summary
	failures      []domain_execution.Summary
	stateMachines []domain_statemachine.StateMachine
	rdsMetrics    []domain_rds.RDSMetric
	lambdaReport  *app_lambda.Report

	credentialError    bool
	credentialErrorMsg string
	activeCount        int

	// Flash tracking (new-item detection)
	flashActiveSeen    map[string]time.Time
	flashCompletedSeen map[string]time.Time
	flashFailuresSeen  map[string]time.Time

	// Change detection hashes (per section)
	hashes map[Section]string

	// Subscribers
	subsMu sync.RWMutex
	subs   map[chan struct{}]struct{}
}

// NewDashboardState creates the centralized state container.
func NewDashboardState() *DashboardState {
	return &DashboardState{
		flashActiveSeen:    make(map[string]time.Time),
		flashCompletedSeen: make(map[string]time.Time),
		flashFailuresSeen:  make(map[string]time.Time),
		hashes:             make(map[Section]string),
		subs:               make(map[chan struct{}]struct{}),
	}
}

// Subscribe returns a latest-only wake-up channel. Notifications do not carry
// state; subscribers read CurrentSnapshot when awakened, so coalescing multiple
// notifications can never discard part of an update.
func (ds *DashboardState) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	ch <- struct{}{} // initial render
	ds.subsMu.Lock()
	ds.subs[ch] = struct{}{}
	ds.subsMu.Unlock()

	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (ds *DashboardState) Unsubscribe(ch chan struct{}) {
	ds.subsMu.Lock()
	defer ds.subsMu.Unlock()
	if _, ok := ds.subs[ch]; !ok {
		return
	}
	delete(ds.subs, ch)
	close(ch)
}

// CurrentSnapshot returns the full state at this moment.
func (ds *DashboardState) CurrentSnapshot() Snapshot {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return ds.fullSnapshotLocked()
}

// notify wakes all subscribers using latest-only semantics.
func (ds *DashboardState) notify() {
	ds.subsMu.RLock()
	defer ds.subsMu.RUnlock()

	for ch := range ds.subs {
		select {
		case ch <- struct{}{}:
		default:
			// A wake-up is already pending; it will read the latest snapshot.
		}
	}
}

// fullSnapshotLocked returns a full snapshot. Caller must hold ds.mu.RLock().
func (ds *DashboardState) fullSnapshotLocked() Snapshot {
	return Snapshot{
		Version:            ds.version,
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
}

// MarkNewExecutions marks executions as New if they haven't been seen before.
// This is the centralized flash/highlight logic.
func (ds *DashboardState) MarkNewExecutions(execs []domain_execution.Summary, seen map[string]time.Time) []domain_execution.Summary {
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
