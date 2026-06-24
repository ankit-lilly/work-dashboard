package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	app_search "github.com/EliLillyCo/work-dashboard/internal/app/search"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
	"github.com/starfederation/datastar-go/datastar"
)

func (s *Server) handleRecordSearch(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)

	query, err := s.parseSearchQuery(r)
	if err != nil {
		s.renderSearchError(sse, query.Email, query.Env, err)
		return
	}

	snapshot := s.searchService.EnsureSearch(r.Context(), query)
	s.renderInitialSearchResults(sse, query, snapshot)
	if query.Email == "" {
		return
	}

	ch := s.searchService.Subscribe(snapshot.Key)
	defer s.searchService.Unsubscribe(snapshot.Key, ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case update, ok := <-ch:
			if !ok {
				return
			}
			s.renderSearchUpdate(sse, query.Email, update)
			if update.Done {
				keepAlive(r.Context(), w)
				return
			}
		}
	}
}

func (s *Server) handleRecordSearchCancel(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		return
	}
	s.searchService.Cancel(key)
	s.renderSearchStopped(sse)
}

func (s *Server) parseSearchQuery(r *http.Request) (app_search.Query, error) {
	if err := r.ParseForm(); err != nil {
		return app_search.Query{}, err
	}
	startAt, endAt, err := s.searchService.ParseDateRange(strings.TrimSpace(r.FormValue("start_date")), strings.TrimSpace(r.FormValue("end_date")))
	if err != nil {
		return app_search.Query{
			Email: strings.TrimSpace(r.FormValue("email_address")),
			Env:   strings.TrimSpace(r.FormValue("env")),
		}, err
	}
	return app_search.Query{
		Email:        strings.TrimSpace(r.FormValue("email_address")),
		Env:          strings.TrimSpace(r.FormValue("env")),
		StateMachine: strings.TrimSpace(r.FormValue("state_machine")),
		StartAt:      startAt,
		EndAt:        endAt,
	}, nil
}

func (s *Server) renderSearchError(sse *datastar.ServerSentEventGenerator, email, env string, err error) {
	html, execErr := s.renderer.ExecuteTemplate("index", "record-search-results", render.SearchStatusData{
		Email:     email,
		SearchKey: "",
		Error:     err.Error(),
		Searching: false,
	})
	if execErr == nil {
		sse.PatchElements(html, datastar.WithUseViewTransitions(false))
	}
}

func (s *Server) renderInitialSearchResults(sse *datastar.ServerSentEventGenerator, query app_search.Query, snapshot app_search.Snapshot) {
	html, err := s.renderer.ExecuteTemplate("index", "record-search-results", render.SearchStatusData{
		Email:     query.Email,
		SearchKey: snapshot.Key,
		Results:   render.PresentSearchResults(snapshot.Results),
		Searching: snapshot.Searching,
		Stopped:   snapshot.Stopped,
	})
	if err == nil {
		sse.PatchElements(html)
	}
}

func (s *Server) renderSearchStopped(sse *datastar.ServerSentEventGenerator) {
	html, err := s.renderer.ExecuteTemplate("index", "record-search-status", render.SearchStatusData{
		Searching: false,
		Stopped:   true,
	})
	if err == nil {
		sse.PatchElements(html, datastar.WithSelector("#record-search-status"), datastar.WithMode(datastar.ElementPatchModeOuter))
	}
}

func (s *Server) renderSearchUpdate(sse *datastar.ServerSentEventGenerator, email string, update app_search.Update) {
	if len(update.NewResults) > 0 {
		html, err := s.renderer.ExecuteTemplate("index", "record-search-rows", map[string]any{
			"Results": render.PresentSearchResults(update.NewResults),
		})
		if err == nil {
			sse.PatchElements(html, datastar.WithSelector("#record-search-rows"), datastar.WithMode(datastar.ElementPatchModeAppend), datastar.WithUseViewTransitions(false))
		}
	}

	statusHTML, err := s.renderer.ExecuteTemplate("index", "record-search-status", render.SearchStatusData{
		Searching: !update.Done,
		Email:     email,
		Progress:  update.Progress,
	})
	if err == nil {
		sse.PatchElements(statusHTML, datastar.WithSelector("#record-search-status"), datastar.WithMode(datastar.ElementPatchModeOuter), datastar.WithUseViewTransitions(false))
	}
}

func keepAlive(ctx context.Context, w http.ResponseWriter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			flusher.Flush()
		}
	}
}
