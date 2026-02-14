package server

import (
	"context"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
)

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

	mux.ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	prodClient, ok := s.awsManager.Clients["prod"]

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	type dashboardData struct {
		activeJobs         []aws.Execution
		recentCompleted    []aws.Execution
		recentFailures     []aws.Execution
		stateMachines      []StateMachineItem
		activeJobsErr      error
		recentCompletedErr error
		recentFailuresErr  error
		stateMachinesErr   error
	}

	resultCh := make(chan dashboardData, 1)
	go func() {
		var data dashboardData
		var wg sync.WaitGroup
		wg.Add(4)

		go func() {
			defer wg.Done()
			if !ok || prodClient == nil {
				return
			}
			data.activeJobs, data.activeJobsErr = prodClient.ListActiveExecutions(ctx)
		}()

		go func() {
			defer wg.Done()
			data.recentCompleted, data.recentCompletedErr = s.fetchRecentCompleted()
		}()

		go func() {
			defer wg.Done()
			if !ok || prodClient == nil {
				return
			}
			data.recentFailures, data.recentFailuresErr = prodClient.ListRecentFailures(ctx)
		}()

		go func() {
			defer wg.Done()
			data.stateMachines, data.stateMachinesErr = s.fetchStateMachines()
		}()

		wg.Wait()
		resultCh <- data
	}()

	select {
	case data := <-resultCh:
		s.render(w, "index", map[string]any{
			"ActiveNav":       "dashboard",
			"ActiveJobs":      data.activeJobs,
			"ActiveJobsError": data.activeJobsErr,
			"ActiveJobsJoke": func() string {
				if len(data.activeJobs) == 0 {
					return s.getFunJoke(ctx)
				}
				return ""
			}(),
			"RecentCompleted":      data.recentCompleted,
			"RecentCompletedError": data.recentCompletedErr,
			"RecentFailures":       data.recentFailures,
			"RecentFailuresError":  data.recentFailuresErr,
			"StateMachines":        data.stateMachines,
			"StateMachinesError":   data.stateMachinesErr,
		})
	case <-ctx.Done():
		s.render(w, "index", map[string]any{
			"ActiveNav": "dashboard",
		})
	}
}
