package server

import (
	"context"
	"crypto/sha256"
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
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	arn := strings.TrimSpace(r.URL.Query().Get("arn"))
	count := parseIntOrDefault(r.URL.Query().Get("count"), 10)
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

	// Load-more is a command only. The persistent dashboard SSE connection is
	// the sole writer for the execution subtree, which prevents an append
	// response racing a full refresh or a newer selection.
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
	if !sess.requestCount(env, arn, count) {
		return
	}

	sse := datastar.NewSSE(w, r)
	_ = sse.MarshalAndPatchSignals(map[string]any{"sm_exec_loading": true})
	select {
	case sess.notify <- struct{}{}:
	default:
	}
}

func (s *Server) handleExecutionStatesModal(w http.ResponseWriter, r *http.Request) {
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

	// All modal opens POST to the same URL, allowing Datastar's automatic
	// request cancellation to abort the previous client stream. The backend
	// generation below provides the same guarantee server-side.
	var signals struct {
		Env     string `json:"env"`
		Arn     string `json:"arn"`
		Request uint64 `json:"request"`
	}
	if err := datastar.ReadSignals(r, &signals); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	env := strings.TrimSpace(signals.Env)
	arn := strings.TrimSpace(signals.Arn)
	if env == "" || arn == "" || signals.Request == 0 {
		http.Error(w, "missing env, arn, or request", http.StatusBadRequest)
		return
	}
	targetID := fmt.Sprintf("states-modal-v-%d", signals.Request)
	ctx, request := sess.beginStatesRequest(r.Context(), env, arn, targetID)
	defer sess.finishStatesRequest(request)

	sse := datastar.NewSSE(w, r)
	if html, err := s.renderer.ExecuteTemplate("index", "states-modal-loading", nil); err == nil {
		sse.PatchElements(html, datastar.WithSelector("#"+targetID), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
	}

	// Fetch and render immediately
	status := s.fetchAndRenderStates(sse, ctx, sess, request)

	// Don't poll if the execution is already in a terminal state.
	if isTerminalStatus(status) {
		return
	}

	// Poll every 5s for live updates while execution is still running.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status = s.fetchAndRenderStates(sse, ctx, sess, request)
			if isTerminalStatus(status) {
				return
			}
		}
	}
}

func (s *Server) handleCancelExecutionStates(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get("X-Session-ID")
	if sid == "" {
		http.Error(w, "missing X-Session-ID header", http.StatusBadRequest)
		return
	}
	if sess := s.sessions.get(sid); sess != nil {
		sess.cancelStatesRequest()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) fetchAndRenderStates(sse *datastar.ServerSentEventGenerator, ctx context.Context, sess *clientSession, request statesRequest) domain_execution.Status {
	fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	stateMachine, status, states, err := s.execService.GetExecutionStates(fetchCtx, request.env, request.arn)
	if ctx.Err() != nil || !sess.statesRequestCurrent(request) {
		return status
	}
	payload := render.ExecutionStatesPayload{
		Env:          request.env,
		ExecutionArn: request.arn,
		StateMachine: stateMachine,
		States:       render.PresentExecutionStates(states),
	}
	if err != nil {
		payload.Error = err.Error()
	}

	html, execErr := s.renderer.ExecuteTemplate("index", "states-modal-content", payload)
	if execErr == nil && sess.statesRequestCurrent(request) {
		sse.PatchElements(html, datastar.WithSelector("#"+request.targetID), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
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

	// The ARN signal is the command. Resolve its environment from backend state
	// so the browser and server cannot disagree about the selected machine.
	var signals struct {
		Arn string `json:"selectedSm"`
	}
	if err := datastar.ReadSignals(r, &signals); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	arn := strings.TrimSpace(signals.Arn)
	env := ""
	if arn != "" {
		var ok bool
		env, ok = s.stateMachineEnv(arn)
		if !ok {
			arn = ""
		}
	}

	changed := sess.selectStateMachine(env, arn)

	// SSE acknowledgement keeps client signals aligned with authoritative state.
	sse := datastar.NewSSE(w, r)
	_ = sse.MarshalAndPatchSignals(map[string]any{
		"selectedSm":      arn,
		"sm_exec_loading": arn != "",
	})

	// Signal the SSE goroutine to fetch now.
	if changed {
		select {
		case sess.notify <- struct{}{}:
		default:
		}
	}
}

func (s *Server) stateMachineEnv(arn string) (string, bool) {
	for _, sm := range s.dashboardState.CurrentSnapshot().StateMachines {
		if sm.Arn == arn {
			return sm.Env, true
		}
	}
	return "", false
}

// execResult carries pre-fetched execution data from the background goroutine
// to the main SSE loop for writing.
type execResult struct {
	view    sessionView
	loading bool   // true = push loading indicator only
	empty   bool   // true = no state machine selected
	html    string // rendered HTML fragment
}

// fetchExecForSession fetches execution list data for the session's selected SM.
// Called from the background exec goroutine — safe to block.
func (s *Server) fetchExecForSession(ctx context.Context, sess *clientSession) execResult {
	view := sess.view()

	// Nothing selected.
	if view.arn == "" {
		const emptyHash = "empty-selection"
		if view.prevHash == emptyHash {
			return execResult{}
		}
		if !sess.commitHash(view, emptyHash) {
			return execResult{}
		}
		return execResult{view: view, empty: true}
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !sess.installCancel(view.generation, cancel) {
		return execResult{}
	}
	defer sess.clearCancel(view.generation)

	details, execErr := s.execService.ListStateMachineExecutions(fetchCtx, view.env, view.arn, view.count)
	items := render.PresentStateMachineExecutions(details)
	total := len(items)
	hasMore := total >= view.count && view.count < 100
	nextCount := min(view.count+10, 100)

	// Content-hash for dedup.
	newHash := hashExecList(items)
	if newHash == "" {
		newHash = "empty" // distinguish "no items" from "never fetched"
	}
	if newHash == view.prevHash {
		return execResult{} // nothing changed
	}

	if !sess.commitHash(view, newHash) {
		return execResult{} // selection changed while AWS was responding
	}

	html, err := s.renderer.ExecuteTemplate("index", "state-machine-executions", map[string]any{
		"Env":        view.env,
		"Arn":        view.arn,
		"Executions": items,
		"Count":      view.count,
		"CountNext":  nextCount,
		"Total":      total,
		"HasMore":    hasMore,
		"Error":      execErr,
	})
	if err != nil {
		return execResult{}
	}
	return execResult{view: view, html: html}
}

// pushExecResult writes a pre-fetched exec result to the SSE stream.
// Called only from the main SSE loop — single-writer guarantee.
func (s *Server) pushExecResult(sse *datastar.ServerSentEventGenerator, sess *clientSession, result execResult) {
	if !sess.isCurrent(result.view) {
		return
	}
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
	b, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
