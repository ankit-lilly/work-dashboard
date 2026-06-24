package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

type executionRepoStub struct {
	refsByStatus map[domain_execution.Status][]domain_execution.Ref
	detail       *domain_execution.Detail
	listErr      error
	detailErr    error
}

func (s executionRepoStub) ListActive(context.Context) ([]domain_execution.Summary, error) {
	return nil, nil
}

func (s executionRepoStub) ListRecentFailed(context.Context) ([]domain_execution.Summary, error) {
	return nil, nil
}

func (s executionRepoStub) ListRecentCompleted(context.Context, time.Time) ([]domain_execution.Summary, error) {
	return nil, nil
}

func (s executionRepoStub) ListStateMachines(context.Context, time.Duration) ([]domain_statemachine.StateMachine, error) {
	return nil, nil
}

func (s executionRepoStub) ListExecutionsByStatus(_ context.Context, _ string, status domain_execution.Status, _ time.Time, _ int) ([]domain_execution.Ref, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.refsByStatus[status], nil
}

func (s executionRepoStub) DescribeExecution(context.Context, string) (*domain_execution.Detail, error) {
	if s.detailErr != nil {
		return nil, s.detailErr
	}
	return s.detail, nil
}

func (s executionRepoStub) GetStateHistory(context.Context, string) ([]domain_execution.State, error) {
	return nil, nil
}

func (s executionRepoStub) ExtractLambdaARNsFromStateMachine(context.Context, string) ([]string, error) {
	return nil, nil
}

func TestListStateMachineExecutionsFallsBackToRefsWhenDescribeFails(t *testing.T) {
	t.Parallel()

	ref := domain_execution.Ref{
		Name:         "exec-1",
		ExecutionArn: "arn:exec-1",
		Status:       domain_execution.StatusSucceeded,
		StartTime:    time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		StopTime:     time.Date(2026, 6, 24, 10, 1, 0, 0, time.UTC),
	}
	svc := NewService(map[string]ExecutionRepository{
		"prod:campprod": executionRepoStub{
			refsByStatus: map[domain_execution.Status][]domain_execution.Ref{
				domain_execution.StatusSucceeded: {ref},
			},
			detailErr: errors.New("describe failed"),
		},
	}, nil, nil)

	items, err := svc.ListStateMachineExecutions(context.Background(), "prod:campprod", "arn:state-machine", 10)
	if err != nil {
		t.Fatalf("ListStateMachineExecutions returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Summary.ExecutionArn != ref.ExecutionArn {
		t.Fatalf("expected execution arn %q, got %q", ref.ExecutionArn, items[0].Summary.ExecutionArn)
	}
	if items[0].Summary.ExecutionName != ref.Name {
		t.Fatalf("expected execution name %q, got %q", ref.Name, items[0].Summary.ExecutionName)
	}
}

func TestListStateMachineExecutionsReturnsListErrorWhenAllStatusesFail(t *testing.T) {
	t.Parallel()

	svc := NewService(map[string]ExecutionRepository{
		"prod:campprod": executionRepoStub{
			listErr: errors.New("list failed"),
		},
	}, nil, nil)

	_, err := svc.ListStateMachineExecutions(context.Background(), "prod:campprod", "arn:state-machine", 10)
	if err == nil {
		t.Fatal("expected an error when all status fetches fail")
	}
}
