package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/starfederation/datastar-go/datastar"
)

type executionStateItem struct {
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

type executionStatesPayload struct {
	Env          string
	ExecutionArn string
	StateMachine string
	States       []executionStateItem
	Error        string
}

func (s *Server) handleExecutionStatesModal(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	arn := strings.TrimSpace(q.Get("arn"))
	targetID := strings.TrimSpace(q.Get("target_id"))
	if env == "" || arn == "" || targetID == "" {
		slog.Warn("execution states missing params", "env", env, "arn", arn, "target_id", targetID)
		return
	}

	client := s.awsManager.Clients[env]
	if client == nil {
		slog.Warn("execution states unknown env", "env", env)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	desc, err := client.Sfn.DescribeExecution(ctx, &sfn.DescribeExecutionInput{
		ExecutionArn: &arn,
	})
	if err != nil {
		var buf bytes.Buffer
		tmpl := s.templateSet("index")
		if tmpl == nil {
			return
		}
		_ = tmpl.ExecuteTemplate(&buf, "states-modal-content", executionStatesPayload{
			Env:          env,
			ExecutionArn: arn,
			Error:        "Failed to describe execution.",
		})
		sse.PatchElements(
			buf.String(),
			datastar.WithSelector("#"+targetID),
			datastar.WithMode(datastar.ElementPatchModeInner),
		)
		return
	}

	items, err := s.buildExecutionStateItems(ctx, client.Sfn, &arn)
	if err != nil {
		var buf bytes.Buffer
		tmpl := s.templateSet("index")
		if tmpl == nil {
			return
		}
		_ = tmpl.ExecuteTemplate(&buf, "states-modal-content", executionStatesPayload{
			Env:          env,
			ExecutionArn: arn,
			Error:        err.Error(),
		})
		sse.PatchElements(
			buf.String(),
			datastar.WithSelector("#"+targetID),
			datastar.WithMode(datastar.ElementPatchModeInner),
		)
		return
	}

	payload := executionStatesPayload{
		Env:          env,
		ExecutionArn: arn,
		StateMachine: derefString(desc.StateMachineArn),
		States:       items,
	}

	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}
	_ = tmpl.ExecuteTemplate(&buf, "states-modal-content", payload)
	sse.PatchElements(
		buf.String(),
		datastar.WithSelector("#"+targetID),
		datastar.WithMode(datastar.ElementPatchModeInner),
	)
}

func (s *Server) buildExecutionStateItems(ctx context.Context, sfnClient *sfn.Client, execArn *string) ([]executionStateItem, error) {
	out, err := sfnClient.GetExecutionHistory(ctx, &sfn.GetExecutionHistoryInput{
		ExecutionArn: execArn,
		MaxResults:   1000,
		ReverseOrder: false,
	})
	if err != nil || out == nil {
		return nil, err
	}

	states := make(map[string]*executionStateItem)
	order := make([]string, 0, 64)

	ensure := func(name string) *executionStateItem {
		if name == "" {
			return nil
		}
		if st, ok := states[name]; ok {
			return st
		}
		st := &executionStateItem{Name: name}
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
			st := ensure(derefString(d.Name))
			if st != nil {
				st.EnterTime = formatTime(ev.Timestamp)
				st.StateType = string(ev.Type)
				if d.Input != nil {
					st.Input = prettyJSONString(*d.Input)
				}
			}
		}
		if d := ev.StateExitedEventDetails; d != nil {
			st := ensure(derefString(d.Name))
			if st != nil {
				st.ExitTime = formatTime(ev.Timestamp)
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
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "FAILED"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		case types.HistoryEventTypeTaskTimedOut:
			if d := ev.TaskTimedOutEventDetails; d != nil {
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "TIMED_OUT"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		case types.HistoryEventTypeExecutionFailed:
			if d := ev.ExecutionFailedEventDetails; d != nil {
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "FAILED"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		case types.HistoryEventTypeLambdaFunctionFailed:
			if d := ev.LambdaFunctionFailedEventDetails; d != nil {
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "FAILED"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		case types.HistoryEventTypeLambdaFunctionTimedOut:
			if d := ev.LambdaFunctionTimedOutEventDetails; d != nil {
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "TIMED_OUT"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		case types.HistoryEventTypeActivityFailed:
			if d := ev.ActivityFailedEventDetails; d != nil {
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "FAILED"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		case types.HistoryEventTypeActivityTimedOut:
			if d := ev.ActivityTimedOutEventDetails; d != nil {
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "TIMED_OUT"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		case types.HistoryEventTypeMapRunFailed:
			if d := ev.MapRunFailedEventDetails; d != nil {
				st := stateFromPrev(evByID, ev.PreviousEventId, ensure)
				if st != nil {
					st.Status = "FAILED"
					st.Error = derefString(d.Error)
					st.Cause = derefString(d.Cause)
				}
			}
		}
	}

	ordered := make([]executionStateItem, 0, len(order))
	for _, name := range order {
		if st := states[name]; st != nil {
			ordered = append(ordered, *st)
		}
	}
	return ordered, nil
}

func stateFromPrev(evByID map[int64]types.HistoryEvent, prevID int64, ensure func(string) *executionStateItem) *executionStateItem {
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
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	v = normalizeJSON(v)
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return string(out)
}
