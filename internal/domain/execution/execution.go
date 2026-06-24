package execution

import "time"

type Status string

const (
	StatusRunning   Status = "RUNNING"
	StatusSucceeded Status = "SUCCEEDED"
	StatusFailed    Status = "FAILED"
	StatusTimedOut  Status = "TIMED_OUT"
	StatusAborted   Status = "ABORTED"
)

type ObjectLocation struct {
	Bucket    string
	Key       string
	Prefix    string
	SizeBytes int64
}

type Summary struct {
	Env           string
	Name          string
	ExecutionName string
	ExecutionArn  string
	Status        Status
	StartTime     time.Time
	StopTime      time.Time
	ErrorType     string
	FailureReason string
	Stale         bool
	New           bool
	Input         ObjectLocation
	Output        ObjectLocation
}

type Ref struct {
	Name         string
	ExecutionArn string
	Status       Status
	StartTime    time.Time
	StopTime     time.Time
}

type MapRun struct {
	Arn       string
	StartTime time.Time
	StopTime  time.Time
}

type Detail struct {
	Summary      Summary
	StateMachine string
	InputRaw     string
	OutputRaw    string
	MapRuns      []MapRun
}

type State struct {
	Name      string
	StateType string
	Status    string
	EnterTime time.Time
	ExitTime  time.Time
	Input     string
	Output    string
	Error     string
	Cause     string
}
