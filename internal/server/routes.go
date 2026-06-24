package server

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	type dashboardData struct {
		activeJobs         []render.ExecutionView
		recentCompleted    []render.ExecutionView
		recentFailures     []render.ExecutionView
		stateMachines      []domain_statemachine.StateMachine
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
			active, err := s.execService.FetchActive()
			data.activeJobs = render.PresentExecutions(active)
			data.activeJobsErr = err
		}()
		go func() {
			defer wg.Done()
			completed, err := s.execService.FetchRecentCompleted()
			data.recentCompleted = render.PresentExecutions(completed)
			data.recentCompletedErr = err
		}()
		go func() {
			defer wg.Done()
			failures, err := s.execService.FetchRecentFailures()
			data.recentFailures = render.PresentExecutions(failures)
			data.recentFailuresErr = err
		}()
		go func() {
			defer wg.Done()
			data.stateMachines, data.stateMachinesErr = s.execService.FetchStateMachines()
		}()

		wg.Wait()
		resultCh <- data
	}()

	select {
	case data := <-resultCh:
		joke := ""
		if len(data.activeJobs) == 0 && s.jokeProvider != nil {
			joke = s.jokeProvider.Random(ctx)
		}
		s.renderer.Render(w, "index", render.DashboardPageData{
			ActiveNav:            "dashboard",
			ActiveJobs:           data.activeJobs,
			ActiveJobsError:      data.activeJobsErr,
			ActiveJobsJoke:       joke,
			RecentCompleted:      data.recentCompleted,
			RecentCompletedError: data.recentCompletedErr,
			RecentFailures:       data.recentFailures,
			RecentFailuresError:  data.recentFailuresErr,
			StateMachines:        data.stateMachines,
			StateMachinesError:   data.stateMachinesErr,
		})
	case <-ctx.Done():
		s.renderer.Render(w, "index", render.DashboardPageData{ActiveNav: "dashboard"})
	}
}

func parseIntOrDefault(val string, def int) int {
	if val == "" {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func sanitizeFilename(filename string) string {
	filename = path.Base(filename)
	filename = strings.ReplaceAll(filename, "\"", "")
	filename = strings.ReplaceAll(filename, "\n", "")
	filename = strings.ReplaceAll(filename, "\r", "")
	filename = strings.ReplaceAll(filename, "\\", "")
	return filename
}
