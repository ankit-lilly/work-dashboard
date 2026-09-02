package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_rds "github.com/EliLillyCo/work-dashboard/internal/domain/rds"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

// hashExecutions computes a content hash for a slice of execution summaries.
func hashExecutions(execs []domain_execution.Summary) string {
	return hashValue(execs)
}

// hashStateMachines computes a content hash for a slice of state machines.
func hashStateMachines(sms []domain_statemachine.StateMachine) string {
	return hashValue(sms)
}

// hashRDSMetrics computes a content hash for a slice of RDS metrics.
func hashRDSMetrics(metrics []domain_rds.RDSMetric) string {
	return hashValue(metrics)
}

// hashLambdaReport computes a content hash for a lambda report.
func hashLambdaReport(report *app_lambda.Report) string {
	return hashValue(report)
}

// hashValue hashes the complete JSON representation of a state section. State
// hashes are correctness guards, so every field that can affect rendering must
// participate; hand-picked field subsets silently lose valid updates.
func hashValue(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		// All state values are JSON-compatible. Returning an empty hash on an
		// unexpected encoding error makes the next successful update observable.
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
