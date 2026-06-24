package state

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

// hashExecutions computes a content hash for a slice of execution summaries.
func hashExecutions(execs []domain_execution.Summary) string {
	if len(execs) == 0 {
		return ""
	}
	h := sha1.New()
	for _, exec := range execs {
		h.Write([]byte(exec.ExecutionArn))
		h.Write([]byte(exec.Status))
		if !exec.StopTime.IsZero() {
			h.Write([]byte(exec.StopTime.UTC().Format(time.RFC3339Nano)))
		}
		if !exec.StartTime.IsZero() {
			h.Write([]byte(exec.StartTime.UTC().Format(time.RFC3339Nano)))
		}
		// Include New flag so flash changes trigger an update
		if exec.New {
			h.Write([]byte("N"))
		}
		if exec.Stale {
			h.Write([]byte("S"))
		}
		h.Write([]byte("|"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashStateMachines computes a content hash for a slice of state machines.
func hashStateMachines(sms []domain_statemachine.StateMachine) string {
	if len(sms) == 0 {
		return ""
	}
	h := sha1.New()
	for _, sm := range sms {
		h.Write([]byte(sm.Env))
		h.Write([]byte(sm.Name))
		h.Write([]byte(sm.Arn))
		h.Write([]byte("|"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashRDSMetrics computes a content hash for a slice of RDS metrics.
func hashRDSMetrics(metrics []domain_rds.RDSMetric) string {
	if len(metrics) == 0 {
		return ""
	}
	h := sha1.New()
	for _, m := range metrics {
		h.Write([]byte(m.Env))
		h.Write([]byte(m.DBInstanceId))
		h.Write([]byte(fmt.Sprintf("%.4f|%.4f|%.4f", m.CPUCurrent, m.CPUAverage, m.CPUMax)))
		h.Write([]byte(fmt.Sprintf("|q%d|dp%d|", len(m.TopQueries), len(m.CPUDataPoints))))
		h.Write([]byte(m.Error))
		h.Write([]byte("|"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashLambdaReport computes a content hash for a lambda report.
func hashLambdaReport(report *app_lambda.Report) string {
	if report == nil || len(report.Metrics) == 0 {
		return ""
	}
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("w%d|c%d|", report.WarningCount, len(report.Metrics))))
	h.Write([]byte(report.LastUpdated.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))
}
