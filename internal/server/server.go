package server

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"time"

	app_execution "github.com/EliLillyCo/work-dashboard/internal/app/execution"
	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	app_rds "github.com/EliLillyCo/work-dashboard/internal/app/rds"
	app_search "github.com/EliLillyCo/work-dashboard/internal/app/search"
	"github.com/EliLillyCo/work-dashboard/internal/broadcaster"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
)

type JokeProvider interface {
	Random(context.Context) string
}

type Server struct {
	cfg           *config.Config
	renderer      *render.Renderer
	execService   *app_execution.Service
	lambdaService *app_lambda.Service
	rdsService    *app_rds.Service
	searchService *app_search.Service
	jokeProvider  JokeProvider
	staticFS      fs.FS

	activeBroadcaster        *broadcaster.Broadcaster[[]domain_execution.Summary]
	failuresBroadcaster      *broadcaster.Broadcaster[[]domain_execution.Summary]
	stateMachinesBroadcaster *broadcaster.Broadcaster[[]domain_statemachine.StateMachine]
	completedBroadcaster     *broadcaster.Broadcaster[[]domain_execution.Summary]
	rdsBroadcaster           *broadcaster.Broadcaster[[]domain_rds.RDSMetric]
	lambdaBroadcaster        *broadcaster.Broadcaster[*app_lambda.Report]
}

func NewServer(execService *app_execution.Service, lambdaService *app_lambda.Service, rdsService *app_rds.Service, searchService *app_search.Service, cfg *config.Config, staticFS fs.FS, jokeProvider JokeProvider) (*Server, error) {
	renderer, err := render.NewRenderer(templatesFS)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:           cfg,
		renderer:      renderer,
		execService:   execService,
		lambdaService: lambdaService,
		rdsService:    rdsService,
		searchService: searchService,
		jokeProvider:  jokeProvider,
		staticFS:      staticFS,
	}
	s.initBroadcasters()
	return s, nil
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
	completedInterval = activeInterval
	failuresInterval = activeInterval

	s.activeBroadcaster = broadcaster.NewBroadcaster(s.execService.FetchActive, activeInterval, "active-executions")
	s.failuresBroadcaster = broadcaster.NewBroadcaster(s.execService.FetchRecentFailures, failuresInterval, "recent-failures")
	s.stateMachinesBroadcaster = broadcaster.NewBroadcaster(s.execService.FetchStateMachines, stateMachinesInterval, "state-machines")
	s.completedBroadcaster = broadcaster.NewBroadcaster(s.execService.FetchRecentCompleted, completedInterval, "recent-completed")
	s.rdsBroadcaster = broadcaster.NewBroadcaster(s.rdsService.FetchMetrics, rdsInterval, "rds-metrics")
	s.lambdaBroadcaster = broadcaster.NewBroadcaster(s.lambdaService.FetchMetrics, lambdaInterval, "lambda-metrics")
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()

	staticRoot, err := fs.Sub(s.staticFS, "static")
	if err != nil {
		http.Error(w, "static assets not available", http.StatusInternalServerError)
		return
	}

	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot))))
	mux.HandleFunc("/api/dashboard-updates", s.handleDashboardUpdates)
	mux.HandleFunc("/api/record-search", s.handleRecordSearch)
	mux.HandleFunc("/api/record-search-cancel", s.handleRecordSearchCancel)
	mux.HandleFunc("/api/state-machine-executions", s.handleStateMachineExecutions)
	mux.HandleFunc("/api/s3-preview-modal", s.handleS3PreviewModal)
	mux.HandleFunc("/api/execution-states", s.handleExecutionStatesModal)
	mux.HandleFunc("/view/json", s.handleS3ViewJSON)
	mux.HandleFunc("/api/s3-download", s.handleS3Download)
	mux.HandleFunc("/api/s3-search", s.handleS3Search)
	mux.HandleFunc("/api/s3-prefix-list", s.handleS3PrefixList)
	mux.HandleFunc("/api/s3-preview", s.handleS3Preview)

	if !strings.HasPrefix(r.URL.Path, "/static/") {
		setNoStoreHeaders(w)
	}

	mux.ServeHTTP(w, r)
}

func setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}
