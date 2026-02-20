package server

import (
	"html/template"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/EliLillyCo/work-dashboard/internal/broadcaster"
	"github.com/EliLillyCo/work-dashboard/internal/config"
)

type Server struct {
	awsManager *aws.ClientManager
	templates  map[string]*template.Template
	staticFS   fs.FS
	cfg        *config.Config
	mu         sync.RWMutex

	activeBroadcaster        *broadcaster.Broadcaster[[]aws.Execution]
	failuresBroadcaster      *broadcaster.Broadcaster[[]aws.Execution]
	stateMachinesBroadcaster *broadcaster.Broadcaster[[]StateMachineItem]
	completedBroadcaster     *broadcaster.Broadcaster[[]aws.Execution]
	rdsBroadcaster           *broadcaster.Broadcaster[[]aws.RDSMetric]
	lambdaBroadcaster        *broadcaster.Broadcaster[*LambdaReport]

	activeCacheMu sync.Mutex
	activeCache   map[string][]aws.Execution
	activeCacheAt map[string]time.Time

	rdsCacheMu sync.Mutex
	rdsCache   map[string][]aws.RDSMetric
	rdsCacheAt map[string]time.Time

	resourceRegistryMu sync.RWMutex
	lambdaRegistry     *LambdaRegistry

	lambdaCacheMu sync.Mutex
	lambdaCache   map[string]cachedLambdaMetrics

	jokeMu      sync.Mutex
	chuckJoke   string
	chuckJokeAt time.Time
	jeffJoke    string
	jeffJokeAt  time.Time

	searchMu        sync.Mutex
	searchStates    map[string]*searchState
	searchStatesTTL time.Duration

	notifyMu            sync.Mutex
	notifyActiveReady   bool
	notifyFailuresReady bool
	seenActive          map[string]time.Time
	seenFailures        map[string]time.Time

	activeIntervalLogged map[string]time.Duration

	credentialErrorMu  sync.RWMutex
	credentialError    bool
	credentialErrorMsg string
	credentialErrorAt  time.Time

	flashActiveSeen    map[string]time.Time
	flashCompletedSeen map[string]time.Time
	flashFailuresSeen  map[string]time.Time

	completedRenderMu   sync.Mutex
	completedRenderHash string
}

func NewServer(awsManager *aws.ClientManager, staticFS fs.FS, cfg *config.Config) *Server {
	searchTTL := 10 * time.Minute
	if cfg != nil && cfg.Limits.SearchStateTTL > 0 {
		searchTTL = cfg.Limits.SearchStateTTL
	}
	s := &Server{
		awsManager:           awsManager,
		staticFS:             staticFS,
		cfg:                  cfg,
		activeCache:          make(map[string][]aws.Execution),
		activeCacheAt:        make(map[string]time.Time),
		rdsCache:             make(map[string][]aws.RDSMetric),
		rdsCacheAt:           make(map[string]time.Time),
		lambdaCache:          make(map[string]cachedLambdaMetrics),
		searchStates:         make(map[string]*searchState),
		searchStatesTTL:      searchTTL,
		seenActive:           make(map[string]time.Time),
		seenFailures:         make(map[string]time.Time),
		activeIntervalLogged: make(map[string]time.Duration),
		flashActiveSeen:      make(map[string]time.Time),
		flashCompletedSeen:   make(map[string]time.Time),
		flashFailuresSeen:    make(map[string]time.Time),
	}
	s.parseTemplates()
	s.initBroadcasters()
	s.startSearchStateCleanup()
	return s
}

func (s *Server) initBroadcasters() {
	activeInterval := 10 * time.Second
	failuresInterval := 180 * time.Second
	stateMachinesInterval := 10 * time.Minute
	completedInterval := 60 * time.Second
	rdsInterval := 30 * time.Second
	lambdaInterval := 60 * time.Second
	if s.cfg != nil {
		activeInterval = s.cfg.Polling.ActiveInterval
		failuresInterval = s.cfg.Polling.FailuresInterval
		stateMachinesInterval = s.cfg.Polling.StateMachinesInterval
		rdsInterval = s.cfg.Polling.RDSFastInterval
	}
	// Keep completed + failures responsive to active updates.
	completedInterval = activeInterval
	failuresInterval = activeInterval
	slog.Info("polling intervals", "active", activeInterval, "failures", failuresInterval, "state_machines", stateMachinesInterval, "completed", completedInterval, "rds", rdsInterval, "lambda", lambdaInterval)
	s.activeBroadcaster = broadcaster.NewBroadcaster(s.fetchActiveExecutions, activeInterval, "active-executions")
	s.failuresBroadcaster = broadcaster.NewBroadcaster(s.fetchRecentFailures, failuresInterval, "recent-failures")
	s.stateMachinesBroadcaster = broadcaster.NewBroadcaster(s.fetchStateMachines, stateMachinesInterval, "state-machines")
	s.completedBroadcaster = broadcaster.NewBroadcaster(s.fetchRecentCompleted, completedInterval, "recent-completed")
	s.rdsBroadcaster = broadcaster.NewBroadcaster(s.fetchRDSMetrics, rdsInterval, "rds-metrics")
	s.lambdaBroadcaster = broadcaster.NewBroadcaster(s.fetchLambdaMetrics, lambdaInterval, "lambda-metrics")
}
