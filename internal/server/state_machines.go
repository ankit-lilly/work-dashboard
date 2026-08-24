package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
	"github.com/starfederation/datastar-go/datastar"
)

func (s *Server) handleStateMachineExecutions(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	arn := strings.TrimSpace(r.URL.Query().Get("arn"))
	count := parseIntOrDefault(r.URL.Query().Get("count"), 10)
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

	// This endpoint only handles one-shot "Load more" appends.
	// Validate the session still matches this request (guards against races).
	if sid := r.Header.Get("X-Session-ID"); sid != "" {
		if sess := s.sessions.get(sid); sess != nil {
			sess.mu.Lock()
			if sess.smArn != arn || sess.smEnv != env {
				sess.mu.Unlock()
				return // stale request — user already switched SM
			}
			sess.smCount = count
			sess.execHash = "" // force re-render with new count
			sess.mu.Unlock()
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	details, _ := s.execService.ListStateMachineExecutions(ctx, env, arn, count)
	items := render.PresentStateMachineExecutions(details)
	total := len(items)
	hasMore := total >= count && count < 100
	nextCount := min(count+10, 100)

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
	status := s.fetchAndRenderStates(sse, r.Context(), env, arn, targetID)

	// Don't poll if the execution is already in a terminal state.
	if isTerminalStatus(status) {
		return
	}

	// Poll every 5s for live updates while execution is still running.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			status = s.fetchAndRenderStates(sse, r.Context(), env, arn, targetID)
			if isTerminalStatus(status) {
				return
			}
		}
	}
}

func (s *Server) fetchAndRenderStates(sse *datastar.ServerSentEventGenerator, ctx context.Context, env, arn, targetID string) domain_execution.Status {
	fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	stateMachine, status, states, err := s.execService.GetExecutionStates(fetchCtx, env, arn)
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
	return status
}

func isTerminalStatus(status domain_execution.Status) bool {
	switch status {
	case domain_execution.StatusSucceeded, domain_execution.StatusFailed, domain_execution.StatusTimedOut, domain_execution.StatusAborted:
		return true
	}
	return false
}

// handleSelectSM is the CQRS command endpoint. The client tells the server
// which state machine it wants to view. The execution list data is then pushed
// via the main dashboard SSE.
func (s *Server) handleSelectSM(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get("X-Session-ID")
	if sid == "" {
		http.Error(w, "missing X-Session-ID header", http.StatusBadRequest)
		return
	}

	sess := s.sessions.get(sid)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Datastar sends signals as JSON body: {"selectedSm": "...", "selectedSmEnv": "..."}
	var signals struct {
		Arn string `json:"selectedSm"`
		Env string `json:"selectedSmEnv"`
	}
	if err := json.NewDecoder(r.Body).Decode(&signals); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	env := strings.TrimSpace(signals.Env)
	arn := strings.TrimSpace(signals.Arn)

	sess.mu.Lock()
	changed := sess.smEnv != env || sess.smArn != arn
	sess.smEnv = env
	sess.smArn = arn
	if changed {
		sess.smCount = 10 // always reset page size on selection change
	}
	sess.execHash = "" // force re-render on next fetch
	sess.mu.Unlock()

	// SSE ack: push immediate loading feedback to the client.
	if arn != "" {
		sse := datastar.NewSSE(w, r)
		sse.PatchSignals([]byte(`{"sm_exec_loading": true}`))
	}

	// Signal the SSE goroutine to fetch now.
	if changed {
		select {
		case sess.notify <- struct{}{}:
		default:
		}
	}
}

// execResult carries pre-fetched execution data from the background goroutine
// to the main SSE loop for writing.
type execResult struct {
	loading bool   // true = push loading indicator only
	empty   bool   // true = no executions found
	html    string // rendered HTML fragment
	env     string // the env this result was fetched for
	arn     string // the arn this result was fetched for
	hash    string // content hash for this result
}

// fetchExecForSession fetches execution list data for the session's selected SM.
// Called from the background exec goroutine — safe to block.
func (s *Server) fetchExecForSession(ctx context.Context, sess *clientSession) execResult {
	sess.mu.Lock()
	env := sess.smEnv
	arn := sess.smArn
	count := sess.smCount
	prevHash := sess.execHash
	sess.mu.Unlock()

	// Nothing selected.
	if arn == "" {
		if prevHash != "" {
			sess.mu.Lock()
			sess.execHash = ""
			sess.mu.Unlock()
			return execResult{empty: true, env: env, arn: arn}
		}
		return execResult{}
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	details, execErr := s.execService.ListStateMachineExecutions(fetchCtx, env, arn, count)
	items := render.PresentStateMachineExecutions(details)
	total := len(items)
	hasMore := total >= count && count < 100
	nextCount := min(count+10, 100)

	// Content-hash for dedup.
	newHash := hashExecList(items)
	if newHash == "" {
		newHash = "empty" // distinguish "no items" from "never fetched"
	}
	if newHash == prevHash {
		return execResult{} // nothing changed
	}

	sess.mu.Lock()
	sess.execHash = newHash
	sess.mu.Unlock()

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
	if err != nil {
		return execResult{}
	}
	return execResult{html: html, env: env, arn: arn, hash: newHash}
}

// pushExecResult writes a pre-fetched exec result to the SSE stream.
// Called only from the main SSE loop — single-writer guarantee.
func (s *Server) pushExecResult(sse *datastar.ServerSentEventGenerator, result execResult) {
	if result.loading {
		html, err := s.renderer.ExecuteTemplate("index", "state-machine-executions-loading", nil)
		if err == nil {
			sse.PatchElements(html, datastar.WithSelector("#state-machine-executions-content"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
		}
		return
	}
	if result.empty {
		s.renderExecEmpty(sse)
		return
	}
	if result.html == "" {
		return // no-op (dedup hit or nothing selected)
	}
	sse.PatchElements(result.html, datastar.WithUseViewTransitions(false))
	sse.PatchSignals([]byte(`{"sm_exec_loading": false}`))
}

// renderExecEmpty pushes the default empty state for the execution list.
func (s *Server) renderExecEmpty(sse *datastar.ServerSentEventGenerator) {
	const emptyHTML = `<div id="state-machine-executions-content" class="p-8 text-center text-sm text-base-content/60">Select a state machine to view executions</div>`
	sse.PatchElements(emptyHTML, datastar.WithUseViewTransitions(false))
	sse.PatchSignals([]byte(`{"sm_exec_loading": false}`))
}

// hashExecList computes a content hash for the rendered execution list (dedup).
func hashExecList(items []render.StateMachineExecutionView) string {
	if len(items) == 0 {
		return ""
	}
	h := sha1.New()
	for _, item := range items {
		h.Write([]byte(item.Arn))
		h.Write([]byte(item.Status))
		h.Write([]byte(item.StartTime))
		h.Write([]byte(item.StopTime))
		h.Write(fmt.Appendf(nil, "|%d|", len(item.MapRuns)))
	}
	return hex.EncodeToString(h.Sum(nil))
}
