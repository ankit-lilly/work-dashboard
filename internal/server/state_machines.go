package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/server/render"
	"github.com/starfederation/datastar-go/datastar"
)

func (s *Server) handleStateMachineExecutions(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	arn := strings.TrimSpace(r.URL.Query().Get("arn"))
	count := parseIntOrDefault(r.URL.Query().Get("count"), 10)
	appendMode := strings.TrimSpace(r.URL.Query().Get("append")) == "1"
	offset := parseIntOrDefault(r.URL.Query().Get("offset"), 0)
	if count < 1 {
		count = 10
	}
	if count > 100 {
		count = 100
	}
	if env == "" || arn == "" {
		http.Error(w, "missing env or arn", http.StatusBadRequest)
		return
	}

	// Append mode: one-shot fetch for "Load more"
	if appendMode {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		details, _ := s.execService.ListStateMachineExecutions(ctx, env, arn, count)
		items := render.PresentStateMachineExecutions(details)
		total := len(items)
		hasMore := total >= count && count < 100
		nextCount := count + 10
		if nextCount > 100 {
			nextCount = 100
		}

		if offset < 0 {
			offset = 0
		}
		if offset > len(items) {
			offset = len(items)
		}
		newItems := items[offset:]
		if len(newItems) > 0 {
			html, err := s.renderer.ExecuteTemplate("index", "state-machine-executions-rows", map[string]any{"Executions": newItems})
			if err == nil {
				sse.PatchElements(html, datastar.WithSelector("#state-machine-executions-rows"), datastar.WithMode(datastar.ElementPatchModeAppend), datastar.WithUseViewTransitions(false))
			}
		}
		html, err := s.renderer.ExecuteTemplate("index", "state-machine-executions-footer", map[string]any{
			"Env":       env,
			"Arn":       arn,
			"Count":     count,
			"CountNext": nextCount,
			"Total":     total,
			"HasMore":   hasMore,
		})
		if err == nil {
			sse.PatchElements(html, datastar.WithSelector("#state-machine-executions-footer"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
		}
		return
	}

	// Show loading indicator
	html, err := s.renderer.ExecuteTemplate("index", "state-machine-executions-loading", nil)
	if err == nil {
		sse.PatchElements(html, datastar.WithSelector("#state-machine-executions-content"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
	}

	// Streaming mode: fetch immediately, then poll every 10s
	s.fetchAndRenderExecutions(sse, r.Context(), env, arn, count)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.fetchAndRenderExecutions(sse, r.Context(), env, arn, count)
		}
	}
}

func (s *Server) fetchAndRenderExecutions(sse *datastar.ServerSentEventGenerator, ctx context.Context, env, arn string, count int) {
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	details, execErr := s.execService.ListStateMachineExecutions(fetchCtx, env, arn, count)
	items := render.PresentStateMachineExecutions(details)
	total := len(items)
	hasMore := total >= count && count < 100
	nextCount := count + 10
	if nextCount > 100 {
		nextCount = 100
	}

	html, err := s.renderer.ExecuteTemplate("index", "state-machine-executions", map[string]any{
		"Env":        env,
		"Arn":        arn,
		"Executions": items,
		"Count":      count,
		"CountNext":  nextCount,
		"Total":      total,
		"HasMore":    hasMore,
		"Error":      execErr,
	})
	if err == nil {
		// Use outer morph (default) — template includes <div id="state-machine-executions-content">.
		// This preserves data-preserve-attr="open" on <details> accordions.
		sse.PatchElements(html, datastar.WithUseViewTransitions(false))
	}
}

func (s *Server) handleExecutionStatesModal(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	arn := strings.TrimSpace(r.URL.Query().Get("arn"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	if env == "" || arn == "" || targetID == "" {
		return
	}

	// Fetch and render immediately
	s.fetchAndRenderStates(sse, r.Context(), env, arn, targetID)

	// Poll every 5s for live updates.
	// When the user opens a different execution, $statesReq increments and the
	// target_id changes. This connection keeps writing to the old target_id which
	// no longer exists in the DOM — Datastar silently ignores it.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.fetchAndRenderStates(sse, r.Context(), env, arn, targetID)
		}
	}
}

func (s *Server) fetchAndRenderStates(sse *datastar.ServerSentEventGenerator, ctx context.Context, env, arn, targetID string) {
	fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	stateMachine, states, err := s.execService.GetExecutionStates(fetchCtx, env, arn)
	payload := render.ExecutionStatesPayload{
		Env:          env,
		ExecutionArn: arn,
		StateMachine: stateMachine,
		States:       render.PresentExecutionStates(states),
	}
	if err != nil {
		payload.Error = err.Error()
	}

	html, execErr := s.renderer.ExecuteTemplate("index", "states-modal-content", payload)
	if execErr == nil {
		sse.PatchElements(html, datastar.WithSelector("#"+targetID), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
	}
}
