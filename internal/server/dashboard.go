package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
	"github.com/EliLillyCo/work-dashboard/internal/state"
	"github.com/starfederation/datastar-go/datastar"
)

func (s *Server) handleDashboardUpdates(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ctx := r.Context()

	ch := s.dashboardState.Subscribe()
	defer s.dashboardState.Unsubscribe(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case snap, ok := <-ch:
			if !ok {
				return
			}
			s.renderSnapshot(sse, snap)
		}
	}
}

// renderSnapshot sends the changed sections of a state snapshot to the client.
func (s *Server) renderSnapshot(sse *datastar.ServerSentEventGenerator, snap state.Snapshot) {
	// Always push credential error state on every update so clients stay in sync.
	if snap.CredentialError {
		sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, snap.CredentialErrorMsg)))
	} else {
		sse.PatchSignals([]byte(`{"credential_error": false, "credential_error_msg": ""}`))
	}

	for _, section := range snap.Changed {
		switch section {
		case state.SectionActive:
			s.renderActiveSection(sse, snap)
		case state.SectionCompleted:
			s.renderCompletedSection(sse, snap)
		case state.SectionFailures:
			s.renderFailuresSection(sse, snap)
		case state.SectionRDS:
			s.renderRDSSection(sse, snap)
		case state.SectionLambda:
			s.renderLambdaSection(sse, snap)
		case state.SectionStateMachines:
			s.renderStateMachineOptions(sse, snap.StateMachines, nil)
		}
	}
}

func (s *Server) renderActiveSection(sse *datastar.ServerSentEventGenerator, snap state.Snapshot) {
	sse.PatchSignals([]byte(fmt.Sprintf(`{"active_jobs_count": %d}`, snap.ActiveCount)))

	activeViews := render.PresentExecutions(snap.Active)
	joke := ""
	if len(activeViews) == 0 && s.jokeProvider != nil {
		joke = s.jokeProvider.Random(context.TODO())
	}
	html, err := s.renderer.ExecuteTemplate("index", "active-jobs", map[string]any{
		"Jobs": activeViews,
		"Joke": joke,
	})
	if err != nil {
		slog.Error("template render failed", "template", "active-jobs", "error", err)
		return
	}
	sse.PatchElements(html, datastar.WithSelector("#active-jobs-list"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
}

func (s *Server) renderCompletedSection(sse *datastar.ServerSentEventGenerator, snap state.Snapshot) {
	sse.PatchSignals([]byte(fmt.Sprintf(`{"recent_completed_count": %d}`, len(snap.Completed))))
	html, err := s.renderer.ExecuteTemplate("index", "recent-completed", map[string]any{
		"Jobs": render.PresentExecutions(snap.Completed),
	})
	if err != nil {
		slog.Error("template render failed", "template", "recent-completed", "error", err)
		return
	}
	// Use outer morph (default) — the template includes <div id="recent-completed-list">.
	// This preserves data-preserve-attr="open" on <details> accordions.
	sse.PatchElements(html, datastar.WithUseViewTransitions(false))
}

func (s *Server) renderFailuresSection(sse *datastar.ServerSentEventGenerator, snap state.Snapshot) {
	sse.PatchSignals([]byte(fmt.Sprintf(`{"recent_failures_count": %d}`, len(snap.Failures))))
	html, err := s.renderer.ExecuteTemplate("index", "recent-failures", map[string]any{
		"Failures": render.PresentExecutions(snap.Failures),
	})
	if err != nil {
		slog.Error("template render failed", "template", "recent-failures", "error", err)
		return
	}
	sse.PatchElements(html, datastar.WithSelector("#recent-failures-list"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
}

func (s *Server) renderRDSSection(sse *datastar.ServerSentEventGenerator, snap state.Snapshot) {
	metricHours := 2
	if s.cfg != nil {
		metricHours = s.cfg.Limits.RDSMetricHours
	}
	sse.PatchSignals([]byte(fmt.Sprintf(`{"rds_loading": false, "rds_db_count": %d}`, len(snap.RDSMetrics))))
	html, err := s.renderer.ExecuteTemplate("index", "rds-metrics", map[string]any{
		"Metrics":     snap.RDSMetrics,
		"MetricHours": metricHours,
	})
	if err != nil {
		slog.Error("template render failed", "template", "rds-metrics", "error", err)
		return
	}
	sse.PatchElements(html, datastar.WithSelector("#rds-metrics-content"), datastar.WithMode(datastar.ElementPatchModeInner))
}

func (s *Server) renderLambdaSection(sse *datastar.ServerSentEventGenerator, snap state.Snapshot) {
	s.renderLambdaReport(sse, snap.LambdaReport)
}

func (s *Server) renderLambdaReport(sse *datastar.ServerSentEventGenerator, report *app_lambda.Report) {
	if report == nil {
		return
	}

	warnings, metrics := render.PresentLambdaReport(report)
	sse.PatchSignals([]byte(fmt.Sprintf(`{"lambda_warnings": %d, "lambda_count": %d}`, len(warnings), len(metrics))))

	warningsHTML, err := s.renderer.ExecuteTemplate("index", "lambda-warnings", map[string]any{"Warnings": warnings})
	if err == nil {
		sse.PatchElements(warningsHTML, datastar.WithSelector("#lambda-warnings-content"), datastar.WithMode(datastar.ElementPatchModeInner))
	}

	metricsHTML, err := s.renderer.ExecuteTemplate("index", "lambda-resources", map[string]any{"Metrics": metrics})
	if err == nil {
		sse.PatchElements(metricsHTML, datastar.WithSelector("#lambda-resources-content"), datastar.WithMode(datastar.ElementPatchModeInner))
	}
}

func (s *Server) renderStateMachineOptions(sse *datastar.ServerSentEventGenerator, sms []domain_statemachine.StateMachine, _ error) {
	type smItem struct {
		Env     string `json:"env"`
		BaseEnv string `json:"baseEnv"`
		Name    string `json:"name"`
		Arn     string `json:"arn"`
	}
	items := make([]smItem, 0, len(sms))
	for _, sm := range sms {
		items = append(items, smItem{
			Env:     sm.Env,
			BaseEnv: sm.BaseEnv,
			Name:    sm.Name,
			Arn:     sm.Arn,
		})
	}
	b, err := json.Marshal(items)
	if err != nil {
		slog.Error("json marshal failed", "context", "state-machine-options", "error", err)
		return
	}
	sse.PatchSignals([]byte(fmt.Sprintf(`{"smList": %s}`, b)))
}
