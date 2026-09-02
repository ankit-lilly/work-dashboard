package state

import (
	"testing"

	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
)

func TestSubscriberWakeupsCoalesceAndReadCurrentState(t *testing.T) {
	ds := NewDashboardState()
	ch := ds.Subscribe()
	defer ds.Unsubscribe(ch)

	<-ch // initial render wake-up

	ds.mu.Lock()
	ds.version = 7
	ds.active = []domain_execution.Summary{{ExecutionArn: "arn:execution:latest"}}
	ds.mu.Unlock()

	ds.notify()
	ds.notify()

	select {
	case <-ch:
	default:
		t.Fatal("expected a pending subscriber wake-up")
	}
	select {
	case <-ch:
		t.Fatal("expected repeated wake-ups to coalesce")
	default:
	}

	snapshot := ds.CurrentSnapshot()
	if snapshot.Version != 7 || len(snapshot.Active) != 1 || snapshot.Active[0].ExecutionArn != "arn:execution:latest" {
		t.Fatalf("subscriber did not observe current state: %#v", snapshot)
	}
}

func TestStateHashesIncludeRenderedFields(t *testing.T) {
	executionsA := []domain_execution.Summary{{ExecutionArn: "arn:execution", FailureReason: "first"}}
	executionsB := []domain_execution.Summary{{ExecutionArn: "arn:execution", FailureReason: "second"}}
	if hashExecutions(executionsA) == hashExecutions(executionsB) {
		t.Fatal("execution hash ignored failure reason")
	}

	rdsA := []domain_rds.RDSMetric{{DBInstanceId: "db", ConnectionPool: domain_rds.ConnectionPoolStats{ActiveConnections: 1}}}
	rdsB := []domain_rds.RDSMetric{{DBInstanceId: "db", ConnectionPool: domain_rds.ConnectionPoolStats{ActiveConnections: 2}}}
	if hashRDSMetrics(rdsA) == hashRDSMetrics(rdsB) {
		t.Fatal("RDS hash ignored connection-pool changes")
	}
}
