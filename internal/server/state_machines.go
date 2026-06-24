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

	if !appendMode {
		html, err := s.renderer.ExecuteTemplate("index", "state-machine-executions-loading", nil)
		if err == nil {
			sse.PatchElements(html, datastar.WithSelector("#state-machine-executions-content"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	details, execErr := s.execService.ListStateMachineExecutions(ctx, env, arn, count)
	items := render.PresentStateMachineExecutions(details)
	total := len(items)
	hasMore := total >= count && count < 100
	nextCount := count + 10
	if nextCount > 100 {
		nextCount = 100
	}

	if appendMode {
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
		sse.PatchElements(html, datastar.WithSelector("#state-machine-executions-content"), datastar.WithMode(datastar.ElementPatchModeInner))
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

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	stateMachine, states, err := s.execService.GetExecutionStates(ctx, env, arn)
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
