package server

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	app_execution "github.com/EliLillyCo/work-dashboard/internal/app/execution"
	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	app_notification "github.com/EliLillyCo/work-dashboard/internal/app/notification"
	app_rds "github.com/EliLillyCo/work-dashboard/internal/app/rds"
	app_search "github.com/EliLillyCo/work-dashboard/internal/app/search"
	"github.com/EliLillyCo/work-dashboard/internal/config"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
	"github.com/EliLillyCo/work-dashboard/internal/state"
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

	// Centralized state — single source of truth for dashboard data.
	dashboardState *state.DashboardState
	orchestrator   *state.Orchestrator
}

func NewServer(execService *app_execution.Service, lambdaService *app_lambda.Service, rdsService *app_rds.Service, searchService *app_search.Service, cfg *config.Config, staticFS fs.FS, jokeProvider JokeProvider, notify *app_notification.Service) (*Server, error) {
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
	s.initState(notify)
	return s, nil
}

func (s *Server) initState(notify *app_notification.Service) {
	ds := state.NewDashboardState()
	s.dashboardState = ds

	var polling *config.PollingConfig
	if s.cfg != nil {
		polling = &s.cfg.Polling
	}

	oCfg := state.OrchestratorConfig{
		FetchActive:        s.execService.FetchActive,
		FetchCompleted:     s.execService.FetchRecentCompleted,
		FetchFailures:      s.execService.FetchRecentFailures,
		FetchStateMachines: s.execService.FetchStateMachines,
		FetchRDS:           s.rdsService.FetchMetrics,
		FetchLambda:        s.lambdaService.FetchMetrics,
		CheckCredentials:   s.execService.CredentialError,
		Notify:             notify,
		Polling:            polling,
	}

	s.orchestrator = state.NewOrchestrator(ds, oCfg)
	s.orchestrator.Start()
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
