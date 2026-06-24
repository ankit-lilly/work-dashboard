package render

import (
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"time"

	app_lambda "github.com/EliLillyCo/work-dashboard/internal/app/lambda"
	app_search "github.com/EliLillyCo/work-dashboard/internal/app/search"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_lambda "github.com/EliLillyCo/work-dashboard/internal/domain/lambda"
	domain_search "github.com/EliLillyCo/work-dashboard/internal/domain/search"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
)

type DashboardPageData struct {
	ActiveNav string

	ActiveJobs      []ExecutionView
	ActiveJobsError error
	ActiveJobsJoke  string

	RecentCompleted      []ExecutionView
	RecentCompletedError error

	RecentFailures      []ExecutionView
	RecentFailuresError error

	StateMachines      []domain_statemachine.StateMachine
	StateMachinesError error
}

type ExecutionView struct {
	Env               string
	Name              string
	ExecutionName     string
	ExecutionArn      string
	Status            string
	StartTime         time.Time
	StopTime          time.Time
	Duration          string
	FailedAt          string
	ErrorType         string
	FailureReason     string
	Stale             bool
	New               bool
	InputBucket       string
	InputKey          string
	InputSize         string
	OutputBucket      string
	OutputPrefix      string
	OutputManifestKey string
}

type StateMachineExecutionView struct {
	Env               string
	Name              string
	Arn               string
	Status            string
	StartTime         string
	StopTime          string
	InputBucket       string
	InputKey          string
	InputSize         string
	OutputBucket      string
	OutputPrefix      string
	OutputManifestKey string
	ErrorType         string
	FailureReason     string
	Duration          string
	MapRuns           []MapRunView
}

type MapRunView struct {
	Arn       string
	StartTime string
	StopTime  string
}

type SearchResultView struct {
	Env           string
	StateMachine  string
	ExecutionName string
	ExecutionArn  string
	StartTime     string
	Bucket        string
	Key           string
	Index         int
	Record        string
	OutputBucket  string
	OutputPrefix  string
	OutputKey     string
}

type SearchStatusData struct {
	SearchKey string
	Email     string
	Results   []SearchResultView
	Error     any
	Searching bool
	Stopped   bool
	Progress  *app_search.Progress
}

type ExecutionStateView struct {
	Name      string
	StateType string
	Status    string
	EnterTime string
	ExitTime  string
	Input     string
	Output    string
	Error     string
	Cause     string
}

type ExecutionStatesPayload struct {
	Env          string
	ExecutionArn string
	StateMachine string
	States       []ExecutionStateView
	Error        string
}

type JSONPreviewData struct {
	Env         string
	Bucket      string
	Key         string
	PreviewHTML template.HTML
	Error       any
	Target      string
}

type S3ObjectItem struct {
	Key          string
	Size         string
	LastModified string
}

type PrefixListData struct {
	Env    string
	Bucket string
	Prefix string
	Items  []S3ObjectItem
	Error  any
	Target string
}

type StringSearchData struct {
	Query   string
	Bucket  string
	Key     string
	Matches []domain_search.StringMatch
	Error   any
	Target  string
}

type LambdaMetricView struct {
	domain_lambda.LambdaMetrics
}

func (v LambdaMetricView) GetTimeoutSeconds() int {
	return int(v.Timeout)
}

func (v LambdaMetricView) FormatAvgDuration() string {
	return domain_lambda.FormatDuration(v.AvgDuration)
}

func (v LambdaMetricView) FormatMaxDuration() string {
	return domain_lambda.FormatDuration(v.MaxDuration)
}

func (v LambdaMetricView) FormatRecentDuration() string {
	return domain_lambda.FormatDuration(v.RecentDuration)
}

func (v LambdaMetricView) FormatTimeout() string {
	seconds := int(v.Timeout)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	remainingSeconds := seconds % 60
	if remainingSeconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, remainingSeconds)
}

func PresentExecutions(items []domain_execution.Summary) []ExecutionView {
	out := make([]ExecutionView, 0, len(items))
	for _, item := range items {
		out = append(out, PresentExecution(item))
	}
	return out
}

func PresentExecution(item domain_execution.Summary) ExecutionView {
	return ExecutionView{
		Env:               item.Env,
		Name:              item.Name,
		ExecutionName:     item.ExecutionName,
		ExecutionArn:      item.ExecutionArn,
		Status:            string(item.Status),
		StartTime:         item.StartTime,
		StopTime:          item.StopTime,
		Duration:          dashboardDuration(item.StartTime, item.StopTime),
		FailedAt:          failedAt(item.StopTime),
		ErrorType:         defaultErrorType(item.ErrorType),
		FailureReason:     item.FailureReason,
		Stale:             item.Stale,
		New:               item.New,
		InputBucket:       item.Input.Bucket,
		InputKey:          item.Input.Key,
		InputSize:         HumanBytes(item.Input.SizeBytes),
		OutputBucket:      item.Output.Bucket,
		OutputPrefix:      item.Output.Prefix,
		OutputManifestKey: item.Output.Key,
	}
}

func PresentStateMachineExecutions(items []domain_execution.Detail) []StateMachineExecutionView {
	out := make([]StateMachineExecutionView, 0, len(items))
	for _, item := range items {
		mapRuns := make([]MapRunView, 0, len(item.MapRuns))
		for _, mr := range item.MapRuns {
			mapRuns = append(mapRuns, MapRunView{
				Arn:       mr.Arn,
				StartTime: FormatTime(mr.StartTime),
				StopTime:  FormatTime(mr.StopTime),
			})
		}
		out = append(out, StateMachineExecutionView{
			Env:               item.Summary.Env,
			Name:              item.Summary.ExecutionName,
			Arn:               item.Summary.ExecutionArn,
			Status:            string(item.Summary.Status),
			StartTime:         FormatTime(item.Summary.StartTime),
			StopTime:          FormatTime(item.Summary.StopTime),
			InputBucket:       item.Summary.Input.Bucket,
			InputKey:          item.Summary.Input.Key,
			InputSize:         HumanBytes(item.Summary.Input.SizeBytes),
			OutputBucket:      item.Summary.Output.Bucket,
			OutputPrefix:      item.Summary.Output.Prefix,
			OutputManifestKey: item.Summary.Output.Key,
			ErrorType:         item.Summary.ErrorType,
			FailureReason:     item.Summary.FailureReason,
			Duration:          stateMachineDuration(item.Summary.StartTime, item.Summary.StopTime),
			MapRuns:           mapRuns,
		})
	}
	return out
}

func PresentSearchResults(items []app_search.Result) []SearchResultView {
	out := make([]SearchResultView, 0, len(items))
	for _, item := range items {
		out = append(out, SearchResultView{
			Env:           item.Env,
			StateMachine:  item.StateMachine,
			ExecutionName: item.ExecutionName,
			ExecutionArn:  item.ExecutionArn,
			StartTime:     FormatTime(item.StartTime),
			Bucket:        item.Bucket,
			Key:           item.Key,
			Index:         item.Index,
			Record:        item.Record,
			OutputBucket:  item.OutputBucket,
			OutputPrefix:  item.OutputPrefix,
			OutputKey:     item.OutputKey,
		})
	}
	return out
}

func PresentExecutionStates(states []domain_execution.State) []ExecutionStateView {
	out := make([]ExecutionStateView, 0, len(states))
	for _, state := range states {
		out = append(out, ExecutionStateView{
			Name:      state.Name,
			StateType: state.StateType,
			Status:    state.Status,
			EnterTime: FormatTime(state.EnterTime),
			ExitTime:  FormatTime(state.ExitTime),
			Input:     state.Input,
			Output:    state.Output,
			Error:     state.Error,
			Cause:     state.Cause,
		})
	}
	return out
}

func PresentPrefixObjects(items []domain_search.ObjectInfo) []S3ObjectItem {
	out := make([]S3ObjectItem, 0, len(items))
	for _, item := range items {
		out = append(out, S3ObjectItem{
			Key:          item.Key,
			Size:         HumanBytes(item.SizeBytes),
			LastModified: FormatTime(item.LastModified),
		})
	}
	return out
}

func PresentLambdaReport(report *app_lambda.Report) (warnings []LambdaMetricView, metrics []LambdaMetricView) {
	if report == nil {
		return []LambdaMetricView{}, []LambdaMetricView{}
	}
	for _, metric := range report.Metrics {
		if metric == nil {
			continue
		}
		view := LambdaMetricView{LambdaMetrics: *metric}
		metrics = append(metrics, view)
		if metric.TimeoutPercent >= domain_lambda.TimeoutWarningHigh {
			warnings = append(warnings, view)
		}
	}
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].TimeoutPercent > metrics[j].TimeoutPercent
	})
	sort.Slice(warnings, func(i, j int) bool {
		return warnings[i].TimeoutPercent > warnings[j].TimeoutPercent
	})
	return warnings, metrics
}

func HumanBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	value := float64(n)
	exp := 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}
	suffixes := []string{"KB", "MB", "GB", "TB"}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + suffixes[exp-1]
}

func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format("2006-01-02 15:04:05 MST")
}

func dashboardDuration(start, stop time.Time) string {
	if start.IsZero() {
		return ""
	}
	if stop.IsZero() {
		return time.Since(start).Round(time.Second).String()
	}
	d := stop.Sub(start)
	if d < 0 {
		d = -d
	}
	return d.Round(time.Second).String()
}

func stateMachineDuration(start, stop time.Time) string {
	if start.IsZero() || stop.IsZero() {
		return ""
	}
	d := stop.Sub(start)
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return strconv.FormatInt(int64(d.Seconds()), 10) + "s"
	}
	if d < time.Hour {
		return strconv.FormatInt(int64(d.Minutes()), 10) + "m"
	}
	return strconv.FormatInt(int64(d.Hours()), 10) + "h"
}

func failedAt(stop time.Time) string {
	if stop.IsZero() {
		return ""
	}
	return stop.In(time.Local).Format("15:04:05 MST (02 Jan)")
}

func defaultErrorType(errorType string) string {
	if errorType == "" {
		return "Execution Failed"
	}
	return errorType
}
