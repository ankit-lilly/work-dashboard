package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
	"github.com/EliLillyCo/work-dashboard/internal/server/render"
	"github.com/starfederation/datastar-go/datastar"
)

func (s *Server) handleDashboardUpdates(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ctx := r.Context()

	activeCh := s.activeBroadcaster.Subscribe()
	defer s.activeBroadcaster.Unsubscribe(activeCh)
	completedCh := s.completedBroadcaster.Subscribe()
	defer s.completedBroadcaster.Unsubscribe(completedCh)
	failuresCh := s.failuresBroadcaster.Subscribe()
	defer s.failuresBroadcaster.Unsubscribe(failuresCh)
	rdsCh := s.rdsBroadcaster.Subscribe()
	defer s.rdsBroadcaster.Unsubscribe(rdsCh)
	lambdaCh := s.lambdaBroadcaster.Subscribe()
	defer s.lambdaBroadcaster.Unsubscribe(lambdaCh)
	smCh := s.stateMachinesBroadcaster.Subscribe()
	defer s.stateMachinesBroadcaster.Unsubscribe(smCh)

	if hasError, errMsg, _ := s.execService.CredentialError(); hasError {
		sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, errMsg)))
	}
	sse.PatchSignals([]byte(`{"rds_loading": true, "rds_db_count": 0, "lambda_warnings": 0, "lambda_count": 0}`))
	s.sendCachedRDSMetrics(sse)
	s.sendCachedLambdaMetrics(sse)
	s.sendCachedStateMachines(sse)

	for {
		select {
		case <-ctx.Done():
			return
		case active, ok := <-activeCh:
			if !ok {
				return
			}
			if hasError, errMsg, _ := s.execService.CredentialError(); hasError {
				sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, errMsg)))
			} else {
				sse.PatchSignals([]byte(`{"credential_error": false, "credential_error_msg": ""}`))
			}
			sse.PatchSignals([]byte(fmt.Sprintf(`{"active_jobs_count": %d}`, len(active))))

			activeViews := render.PresentExecutions(active)
			joke := ""
			if len(activeViews) == 0 && s.jokeProvider != nil {
				joke = s.jokeProvider.Random(ctx)
			}
			html, err := s.renderer.ExecuteTemplate("index", "active-jobs", map[string]any{
				"Jobs": activeViews,
				"Joke": joke,
			})
			if err != nil {
				return
			}
			sse.PatchElements(html, datastar.WithSelector("#active-jobs-list"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
		case completed, ok := <-completedCh:
			if !ok {
				return
			}
			if s.execService.IsSameCompletedSnapshot(completed) {
				continue
			}
			sse.PatchSignals([]byte(fmt.Sprintf(`{"recent_completed_count": %d}`, len(completed))))
			html, err := s.renderer.ExecuteTemplate("index", "recent-completed", map[string]any{
				"Jobs": render.PresentExecutions(completed),
			})
			if err != nil {
				slog.Error("template render failed", "template", "recent-completed", "error", err)
				continue
			}
			sse.PatchElements(html, datastar.WithSelector("#recent-completed-list"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
		case failures, ok := <-failuresCh:
			if !ok {
				return
			}
			sse.PatchSignals([]byte(fmt.Sprintf(`{"recent_failures_count": %d}`, len(failures))))
			html, err := s.renderer.ExecuteTemplate("index", "recent-failures", map[string]any{
				"Failures": render.PresentExecutions(failures),
			})
			if err != nil {
				slog.Error("template render failed", "template", "recent-failures", "error", err)
				continue
			}
			sse.PatchElements(html, datastar.WithSelector("#recent-failures-list"), datastar.WithMode(datastar.ElementPatchModeInner), datastar.WithUseViewTransitions(false))
		case metrics, ok := <-rdsCh:
			if !ok {
				return
			}
			metricHours := 2
			if s.cfg != nil {
				metricHours = s.cfg.Limits.RDSMetricHours
			}
			sse.PatchSignals([]byte(fmt.Sprintf(`{"rds_loading": false, "rds_db_count": %d}`, len(metrics))))
			html, err := s.renderer.ExecuteTemplate("index", "rds-metrics", map[string]any{
				"Metrics":     metrics,
				"MetricHours": metricHours,
			})
			if err != nil {
				slog.Error("template render failed", "template", "rds-metrics", "error", err)
				continue
			}
			sse.PatchElements(html, datastar.WithSelector("#rds-metrics-content"), datastar.WithMode(datastar.ElementPatchModeInner))
		case report, ok := <-lambdaCh:
			if !ok {
				return
			}
			s.renderLambdaReport(sse, report)
		case sms, ok := <-smCh:
			if !ok {
				return
			}
			s.renderStateMachineOptions(sse, sms, nil)
		}
	}
}

func (s *Server) renderLambdaReport(sse *datastar.ServerSentEventGenerator, report *app_lambda.Report) {
	hasCredError, credMsg, _ := s.execService.CredentialError()
	if hasCredError {
		sse.PatchSignals([]byte(fmt.Sprintf(`{"credential_error": true, "credential_error_msg": %q}`, credMsg)))
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

func (s *Server) sendCachedRDSMetrics(sse *datastar.ServerSentEventGenerator) {
	metrics := s.rdsService.CachedMetrics()
	if len(metrics) == 0 {
		return
	}
	metricHours := 2
	if s.cfg != nil {
		metricHours = s.cfg.Limits.RDSMetricHours
	}
	sse.PatchSignals([]byte(fmt.Sprintf(`{"rds_loading": false, "rds_db_count": %d}`, len(metrics))))
	html, err := s.renderer.ExecuteTemplate("index", "rds-metrics", map[string]any{
		"Metrics":     metrics,
		"MetricHours": metricHours,
	})
	if err == nil {
		sse.PatchElements(html, datastar.WithSelector("#rds-metrics-content"), datastar.WithMode(datastar.ElementPatchModeInner))
	}
}

func (s *Server) sendCachedLambdaMetrics(sse *datastar.ServerSentEventGenerator) {
	s.renderLambdaReport(sse, s.lambdaService.CachedReport())
}

func (s *Server) sendCachedStateMachines(sse *datastar.ServerSentEventGenerator) {
	sms, ok := s.stateMachinesBroadcaster.Current()
	if !ok || len(sms) == 0 {
		return
	}
	s.renderStateMachineOptions(sse, sms, nil)
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
