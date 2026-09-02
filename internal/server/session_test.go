package server

import (
	"context"
	"strings"
	"testing"

	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
)

func TestSessionRejectsResultsFromAnOlderSelection(t *testing.T) {
	sess := &clientSession{smCount: 10, notify: make(chan struct{}, 1)}
	if !sess.selectStateMachine("dev", "arn:machine:a") {
		t.Fatal("expected first selection to change the session")
	}
	viewA := sess.view()

	fetchCtx, cancel := context.WithCancel(context.Background())
	if !sess.installCancel(viewA.generation, cancel) {
		t.Fatal("expected cancellation to install for current generation")
	}

	if !sess.selectStateMachine("qa", "arn:machine:b") {
		t.Fatal("expected second selection to change the session")
	}
	select {
	case <-fetchCtx.Done():
	default:
		t.Fatal("changing selection did not cancel the older fetch")
	}
	if sess.commitHash(viewA, "stale-result") {
		t.Fatal("older selection was allowed to commit its result")
	}
	if sess.isCurrent(viewA) {
		t.Fatal("older selection was still considered current")
	}

	viewB := sess.view()
	if viewB.arn != "arn:machine:b" || viewB.env != "qa" || viewB.generation <= viewA.generation {
		t.Fatalf("unexpected current view: %#v", viewB)
	}
}

func TestLoadMoreBelongsToCurrentSelection(t *testing.T) {
	sess := &clientSession{smCount: 10, notify: make(chan struct{}, 1)}
	sess.selectStateMachine("dev", "arn:machine:a")

	if sess.requestCount("qa", "arn:machine:b", 20) {
		t.Fatal("accepted load-more command for another selection")
	}
	if !sess.requestCount("dev", "arn:machine:a", 20) {
		t.Fatal("did not accept load-more for current selection")
	}
	if view := sess.view(); view.count != 20 {
		t.Fatalf("load-more count was not maintained: %#v", view)
	}
}

func TestNewStatesRequestCancelsAndInvalidatesOlderRequest(t *testing.T) {
	sess := &clientSession{smCount: 10, notify: make(chan struct{}, 1)}
	ctxA, requestA := sess.beginStatesRequest(context.Background(), "dev", "arn:execution:a", "states-modal-v-1")
	ctxB, requestB := sess.beginStatesRequest(context.Background(), "qa", "arn:execution:b", "states-modal-v-2")

	select {
	case <-ctxA.Done():
	default:
		t.Fatal("starting a newer modal request did not cancel the older request")
	}
	if sess.statesRequestCurrent(requestA) {
		t.Fatal("older modal request was still considered current")
	}
	if !sess.statesRequestCurrent(requestB) {
		t.Fatal("newer modal request was not considered current")
	}

	// An older handler finishing must not cancel the newer handler.
	sess.finishStatesRequest(requestA)
	select {
	case <-ctxB.Done():
		t.Fatal("older handler cleanup cancelled the newer request")
	default:
	}

	sess.cancelStatesRequest()
	select {
	case <-ctxB.Done():
	default:
		t.Fatal("closing the modal did not cancel its request")
	}
	if sess.statesRequestCurrent(requestB) {
		t.Fatal("closed modal request was still considered current")
	}
}

func TestStateMachineOptionsAreServerRendered(t *testing.T) {
	renderer, err := render.NewRenderer(templatesFS)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	items := []domain_statemachine.StateMachine{
		{Env: "dev:campdev", BaseEnv: "dev", Name: "RulesProcessor", Arn: "arn:machine:selected"},
		{Env: "qa:campqa", BaseEnv: "qa", Name: "DataProcessor", Arn: "arn:machine:other"},
	}
	html, err := renderer.ExecuteTemplate("index", "state-machine-options", map[string]any{
		"StateMachines": items,
		"Selected":      "arn:machine:selected",
	})
	if err != nil {
		t.Fatalf("render state-machine options: %v", err)
	}
	if !strings.Contains(html, "arn:machine:selected") || !strings.Contains(html, "selected") || !strings.Contains(html, "data-show") {
		t.Fatalf("state-machine options missing server state: %s", html)
	}
	if strings.Contains(html, "document.") || strings.Contains(html, "fetch(") {
		t.Fatalf("state-machine options contain custom JavaScript: %s", html)
	}
}

func TestStatesViewerUsesOneDatastarActionURL(t *testing.T) {
	renderer, err := render.NewRenderer(templatesFS)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	html, err := renderer.ExecuteTemplate("index", "states-viewer-link", map[string]string{
		"Env":          "dev",
		"ExecutionArn": "arn:execution:test",
	})
	if err != nil {
		t.Fatalf("render states viewer link: %v", err)
	}
	if !strings.Contains(html, "@post") || !strings.Contains(html, "/api/execution-states") || !strings.Contains(html, "payload") {
		t.Fatalf("states viewer does not use the shared Datastar action: %s", html)
	}
	if strings.Contains(html, "/api/execution-states?") || strings.Contains(html, "target_id") {
		t.Fatalf("states viewer still creates a request-specific action URL: %s", html)
	}
}

func TestExecutionListHashIncludesRenderedDetails(t *testing.T) {
	a := []render.StateMachineExecutionView{{Arn: "arn:execution", Duration: "1s", FailureReason: "first"}}
	b := []render.StateMachineExecutionView{{Arn: "arn:execution", Duration: "2s", FailureReason: "first"}}
	if hashExecList(a) == hashExecList(b) {
		t.Fatal("execution list hash ignored rendered duration")
	}
}
