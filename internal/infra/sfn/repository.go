package sfn

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"time"

	legacyaws "github.com/EliLillyCo/work-dashboard/internal/aws"
	domain_execution "github.com/EliLillyCo/work-dashboard/internal/domain/execution"
	domain_lambda "github.com/EliLillyCo/work-dashboard/internal/domain/lambda"
	domain_statemachine "github.com/EliLillyCo/work-dashboard/internal/domain/statemachine"
	"github.com/EliLillyCo/work-dashboard/internal/infra/awsclient"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

type Repository struct {
	client *awsclient.AWSClient
}

func NewRepository(client *awsclient.AWSClient) *Repository {
	return &Repository{client: client}
}

func (r *Repository) ListActive(ctx context.Context) ([]domain_execution.Summary, error) {
	executions, err := r.client.ListActiveExecutions(ctx)
	if err != nil {
		return nil, err
	}
	return mapExecutionSummaries(executions), nil
}

func (r *Repository) ListRecentFailed(ctx context.Context) ([]domain_execution.Summary, error) {
	executions, err := r.client.ListRecentFailures(ctx)
	if err != nil {
		return nil, err
	}
	return mapExecutionSummaries(executions), nil
}

func (r *Repository) ListRecentCompleted(ctx context.Context, since time.Time) ([]domain_execution.Summary, error) {
	executions, err := r.client.ListRecentCompleted(ctx, since)
	if err != nil {
		return nil, err
	}
	return mapExecutionSummaries(executions), nil
}

func (r *Repository) ListStateMachines(ctx context.Context, ttl time.Duration) ([]domain_statemachine.StateMachine, error) {
	items, err := r.client.ListFilteredStateMachines(ctx, ttl)
	if err != nil {
		return nil, err
	}

	stateMachines := make([]domain_statemachine.StateMachine, 0, len(items))
	for _, item := range items {
		stateMachines = append(stateMachines, domain_statemachine.StateMachine{
			Env:     r.client.EnvName,
			BaseEnv: domain_statemachine.BaseEnvFromKey(strings.ToLower(r.client.EnvName)),
			Name:    awsclient.DerefString(item.Name),
			Arn:     awsclient.DerefString(item.StateMachineArn),
		})
	}
	return stateMachines, nil
}

func (r *Repository) ListExecutionsByStatus(ctx context.Context, stateMachineArn string, status domain_execution.Status, cutoff time.Time, maxPages int) ([]domain_execution.Ref, error) {
	items, err := r.client.ListRecentExecutionsByStatus(ctx, &stateMachineArn, toAWSStatus(status), cutoff, maxPages)
	if err != nil {
		return nil, err
	}

	refs := make([]domain_execution.Ref, 0, len(items))
	for _, item := range items {
		refs = append(refs, domain_execution.Ref{
			Name:         awsclient.DerefString(item.Name),
			ExecutionArn: awsclient.DerefString(item.ExecutionArn),
			Status:       domain_execution.Status(item.Status),
			StartTime:    derefTime(item.StartDate),
			StopTime:     derefTime(item.StopDate),
		})
	}
	return refs, nil
}

func (r *Repository) DescribeExecution(ctx context.Context, executionArn string) (*domain_execution.Detail, error) {
	desc, err := r.client.Sfn.DescribeExecution(ctx, &awssfn.DescribeExecutionInput{
		ExecutionArn: &executionArn,
	})
	if err != nil {
		return nil, err
	}

	bucket, key, prefix := extractBucketKeyPrefix(desc.Input)
	outBucket, outManifestKey := extractResultWriterDetails(desc.Output)
	outPrefix := prefix
	if outBucket != "" && outManifestKey != "" {
		outPrefix = strings.TrimSuffix(outManifestKey, path.Base(outManifestKey))
	}

	var inputSize int64
	if bucket != "" && key != "" {
		head, err := r.client.S3.HeadObject(ctx, &awss3.HeadObjectInput{
			Bucket: &bucket,
			Key:    &key,
		})
		if err == nil && head.ContentLength != nil {
			inputSize = *head.ContentLength
		}
	}

	summary := domain_execution.Summary{
		Env:           r.client.EnvName,
		ExecutionArn:  executionArn,
		Status:        domain_execution.Status(desc.Status),
		StartTime:     derefTime(desc.StartDate),
		StopTime:      derefTime(desc.StopDate),
		ErrorType:     awsclient.DerefString(desc.Error),
		FailureReason: awsclient.DerefString(desc.Cause),
		Input: domain_execution.ObjectLocation{
			Bucket:    bucket,
			Key:       key,
			Prefix:    prefix,
			SizeBytes: inputSize,
		},
		Output: domain_execution.ObjectLocation{
			Bucket: outBucket,
			Key:    outManifestKey,
			Prefix: outPrefix,
		},
	}

	mapRuns := make([]domain_execution.MapRun, 0)
	if desc.Status == types.ExecutionStatusSucceeded {
		out, err := r.client.Sfn.ListMapRuns(ctx, &awssfn.ListMapRunsInput{
			ExecutionArn: &executionArn,
			MaxResults:   10,
		})
		if err == nil {
			for _, item := range out.MapRuns {
				mapRuns = append(mapRuns, domain_execution.MapRun{
					Arn:       awsclient.DerefString(item.MapRunArn),
					StartTime: derefTime(item.StartDate),
					StopTime:  derefTime(item.StopDate),
				})
			}
		}
	}

	return &domain_execution.Detail{
		Summary:      summary,
		StateMachine: awsclient.DerefString(desc.StateMachineArn),
		InputRaw:     awsclient.DerefString(desc.Input),
		OutputRaw:    awsclient.DerefString(desc.Output),
		MapRuns:      mapRuns,
	}, nil
}

func (r *Repository) GetStateHistory(ctx context.Context, executionArn string) ([]domain_execution.State, error) {
	out, err := r.client.Sfn.GetExecutionHistory(ctx, &awssfn.GetExecutionHistoryInput{
		ExecutionArn: &executionArn,
		MaxResults:   1000,
		ReverseOrder: false,
	})
	if err != nil || out == nil {
		return nil, err
	}

	states := make(map[string]*domain_execution.State)
	order := make([]string, 0, 64)
	ensure := func(name string) *domain_execution.State {
		if name == "" {
			return nil
		}
		if st, ok := states[name]; ok {
			return st
		}
		st := &domain_execution.State{Name: name}
		states[name] = st
		order = append(order, name)
		return st
	}

	evByID := make(map[int64]types.HistoryEvent, len(out.Events))
	for _, ev := range out.Events {
		evByID[ev.Id] = ev
	}

	for _, ev := range out.Events {
		if d := ev.StateEnteredEventDetails; d != nil {
			st := ensure(awsclient.DerefString(d.Name))
			if st != nil {
				st.EnterTime = derefTime(ev.Timestamp)
				st.StateType = string(ev.Type)
				if d.Input != nil {
					st.Input = prettyJSONString(*d.Input)
				}
			}
		}
		if d := ev.StateExitedEventDetails; d != nil {
			st := ensure(awsclient.DerefString(d.Name))
			if st != nil {
				st.ExitTime = derefTime(ev.Timestamp)
				if st.StateType == "" {
					st.StateType = string(ev.Type)
				}
				if d.Output != nil {
					st.Output = prettyJSONString(*d.Output)
				}
			}
		}
		switch ev.Type {
		case types.HistoryEventTypeTaskFailed:
			if d := ev.TaskFailedEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "FAILED", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		case types.HistoryEventTypeTaskTimedOut:
			if d := ev.TaskTimedOutEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "TIMED_OUT", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		case types.HistoryEventTypeExecutionFailed:
			if d := ev.ExecutionFailedEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "FAILED", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		case types.HistoryEventTypeLambdaFunctionFailed:
			if d := ev.LambdaFunctionFailedEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "FAILED", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		case types.HistoryEventTypeLambdaFunctionTimedOut:
			if d := ev.LambdaFunctionTimedOutEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "TIMED_OUT", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		case types.HistoryEventTypeActivityFailed:
			if d := ev.ActivityFailedEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "FAILED", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		case types.HistoryEventTypeActivityTimedOut:
			if d := ev.ActivityTimedOutEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "TIMED_OUT", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		case types.HistoryEventTypeMapRunFailed:
			if d := ev.MapRunFailedEventDetails; d != nil {
				setFailureState(evByID, ev.PreviousEventId, ensure, "FAILED", awsclient.DerefString(d.Error), awsclient.DerefString(d.Cause))
			}
		}
	}

	ordered := make([]domain_execution.State, 0, len(order))
	for _, name := range order {
		if st := states[name]; st != nil {
			ordered = append(ordered, *st)
		}
	}
	return ordered, nil
}

func (r *Repository) ExtractLambdaARNsFromStateMachine(ctx context.Context, stateMachineArn string) ([]string, error) {
	return r.client.ExtractLambdaFunctionsFromStateMachine(ctx, stateMachineArn)
}

func (r *Repository) ExtractLambdaInvocations(ctx context.Context, executionArn string, stateMachineName string) (map[string]*domain_lambda.LambdaResource, error) {
	items, err := r.client.ExtractLambdaResourcesFromExecution(ctx, executionArn, stateMachineName)
	if err != nil {
		return nil, err
	}

	resources := make(map[string]*domain_lambda.LambdaResource, len(items))
	for key, item := range items {
		invocations := make([]domain_lambda.InvocationRecord, 0, len(item.Invocations))
		for _, invocation := range item.Invocations {
			invocations = append(invocations, domain_lambda.InvocationRecord{
				ExecutionArn: invocation.ExecutionArn,
				StateName:    invocation.StateName,
				Duration:     invocation.Duration,
				TimedOut:     invocation.TimedOut,
				Failed:       invocation.Failed,
				Timestamp:    invocation.Timestamp,
			})
		}
		resources[key] = &domain_lambda.LambdaResource{
			FunctionName: item.FunctionName,
			FunctionArn:  item.FunctionArn,
			Invocations:  invocations,
			LastSeen:     item.LastSeen,
		}
	}
	return resources, nil
}

func mapExecutionSummaries(items []legacyaws.Execution) []domain_execution.Summary {
	out := make([]domain_execution.Summary, 0, len(items))
	for _, item := range items {
		out = append(out, domain_execution.Summary{
			Env:           item.Env,
			Name:          item.Name,
			ExecutionName: item.ExecutionName,
			ExecutionArn:  item.ExecutionArn,
			Status:        domain_execution.Status(item.Status),
			StartTime:     item.StartTime,
			StopTime:      item.StopTime,
			ErrorType:     item.ErrorType,
			FailureReason: item.FailureReason,
			Stale:         item.Stale,
			New:           item.New,
			Input: domain_execution.ObjectLocation{
				Bucket: item.InputBucket,
				Key:    item.InputKey,
			},
			Output: domain_execution.ObjectLocation{
				Bucket: item.OutputBucket,
				Key:    item.OutputManifestKey,
				Prefix: item.OutputPrefix,
			},
		})
	}
	return out
}

func toAWSStatus(status domain_execution.Status) types.ExecutionStatus {
	switch status {
	case domain_execution.StatusRunning:
		return types.ExecutionStatusRunning
	case domain_execution.StatusSucceeded:
		return types.ExecutionStatusSucceeded
	case domain_execution.StatusFailed:
		return types.ExecutionStatusFailed
	case domain_execution.StatusTimedOut:
		return types.ExecutionStatusTimedOut
	case domain_execution.StatusAborted:
		return types.ExecutionStatusAborted
	default:
		return types.ExecutionStatus(status)
	}
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func setFailureState(evByID map[int64]types.HistoryEvent, prevID int64, ensure func(string) *domain_execution.State, status, errText, cause string) {
	st := stateFromPrev(evByID, prevID, ensure)
	if st == nil {
		return
	}
	st.Status = status
	st.Error = errText
	st.Cause = cause
}

func stateFromPrev(evByID map[int64]types.HistoryEvent, prevID int64, ensure func(string) *domain_execution.State) *domain_execution.State {
	if prevID == 0 {
		return nil
	}
	for prevID != 0 {
		ev, ok := evByID[prevID]
		if !ok {
			return nil
		}
		if ev.StateEnteredEventDetails != nil && ev.StateEnteredEventDetails.Name != nil {
			return ensure(*ev.StateEnteredEventDetails.Name)
		}
		if ev.StateExitedEventDetails != nil && ev.StateExitedEventDetails.Name != nil {
			return ensure(*ev.StateExitedEventDetails.Name)
		}
		prevID = ev.PreviousEventId
	}
	return nil
}

func prettyJSONString(raw string) string {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	value = normalizeJSON(value)
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return raw
	}
	return string(out)
}

func normalizeJSON(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		for key, value := range typed {
			typed[key] = normalizeJSON(value)
		}
		return typed
	case []any:
		for idx := range typed {
			typed[idx] = normalizeJSON(typed[idx])
		}
		return typed
	case string:
		s := strings.TrimSpace(typed)
		if len(s) >= 2 && ((s[0] == '{' && s[len(s)-1] == '}') || (s[0] == '[' && s[len(s)-1] == ']')) {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return normalizeJSON(parsed)
			}
		}
		return typed
	default:
		return v
	}
}

func extractBucketKeyPrefix(input *string) (string, string, string) {
	if input == nil || strings.TrimSpace(*input) == "" {
		return "", "", ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(*input), &payload); err != nil {
		return "", "", ""
	}

	if bucket, key, prefix := getBucketKeyPrefix(payload); bucket != "" {
		return bucket, key, prefix
	}

	if child, ok := payload["Payload"].(map[string]any); ok {
		return getBucketKeyPrefix(child)
	}

	return "", "", ""
}

func getBucketKeyPrefix(payload map[string]any) (string, string, string) {
	bucket, _ := payload["Bucket"].(string)
	key, _ := payload["Key"].(string)
	prefix, _ := payload["Prefix"].(string)
	if bucket == "" {
		return "", "", ""
	}
	return bucket, key, prefix
}

func extractResultWriterDetails(output *string) (string, string) {
	if output == nil || strings.TrimSpace(*output) == "" {
		return "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(*output), &payload); err != nil {
		return "", ""
	}
	if details, ok := payload["ResultWriterDetails"].(map[string]any); ok {
		bucket, _ := details["Bucket"].(string)
		key, _ := details["Key"].(string)
		return bucket, key
	}
	return "", ""
}
