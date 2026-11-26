package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

func identityToProto(id session.Identity) *agentmaticv1.UserIdentity {
	pb := &agentmaticv1.UserIdentity{
		ClusterId: id.ClusterID,
		UserUid:   id.UserUID,
		Username:  id.Username,
		Groups:    append([]string(nil), id.Groups...),
	}
	if id.DisplayName != "" {
		name := id.DisplayName
		pb.DisplayName = &name
	}
	if id.Email != "" {
		email := id.Email
		pb.Email = &email
	}
	return pb
}

func sessionSummaryToProto(s *session.Session) *agentmaticv1.SessionSummary {
	snap := s.Snapshot()
	turns := extractTurns(snap.History)

	pb := &agentmaticv1.SessionSummary{
		Id:              snap.ID,
		Title:           snap.Title,
		Status:          string(snap.Status),
		UpdatedAtUnixMs: snap.UpdatedAt.UnixMilli(),
		TurnCount:       int32(len(turns)),
		Owner:           identityToProto(snap.Owner),
	}
	if snap.Name != "" {
		name := snap.Name
		pb.Name = &name
	}
	return pb
}

func sessionDetailsToProto(s *session.Session) *agentmaticv1.SessionDetails {
	snap := s.Snapshot()
	turns := extractTurns(snap.History)

	pb := &agentmaticv1.SessionDetails{
		Id:              snap.ID,
		Title:           snap.Title,
		Status:          string(snap.Status),
		CreatedAtUnixMs: snap.CreatedAt.UnixMilli(),
		UpdatedAtUnixMs: snap.UpdatedAt.UnixMilli(),
		TurnCount:       int32(len(turns)),
		Turns:           turns,
		Owner:           identityToProto(snap.Owner),
	}
	if snap.Name != "" {
		name := snap.Name
		pb.Name = &name
	}
	return pb
}

// extractTurns groups multi-step message history into user-prompt -> assistant-answer turn pairs.
func extractTurns(history []llm.Message) []*agentmaticv1.Turn {
	var turns []*agentmaticv1.Turn
	var current *agentmaticv1.Turn

	for _, msg := range history {
		switch msg.Role {
		case llm.RoleUser:
			if current != nil {
				turns = append(turns, current)
			}
			current = &agentmaticv1.Turn{
				TurnIndex:  int32(len(turns)),
				UserPrompt: msg.Text(),
			}
		case llm.RoleAssistant:
			if current != nil {
				if text := msg.Text(); text != "" {
					current.AssistantResponse = text
				}
			}
		}
	}
	if current != nil {
		turns = append(turns, current)
	}
	return turns
}

// stepToExecutionEvents splits a single engine.Step into chronological streaming events
// (optional thinking, tool call dispatch, and tool result observation).
func stepToExecutionEvents(step engine.Step, nowMs int64) []*agentmaticv1.ExecutionEvent {
	var events []*agentmaticv1.ExecutionEvent
	stepIdx := int32(step.Number)

	if thought := strings.TrimSpace(step.Thinking); thought != "" {
		events = append(events, &agentmaticv1.ExecutionEvent{
			StepIndex:       stepIdx,
			TimestampUnixMs: nowMs,
			Payload: &agentmaticv1.ExecutionEvent_Thinking{
				Thinking: &agentmaticv1.ThinkingEvent{
					Thought: thought,
				},
			},
		})
	}

	var callID, toolName string
	if step.Action != nil {
		callID = step.Action.ID
		toolName = step.Action.Name
		events = append(events, &agentmaticv1.ExecutionEvent{
			StepIndex:       stepIdx,
			TimestampUnixMs: nowMs,
			Payload: &agentmaticv1.ExecutionEvent_ToolCall{
				ToolCall: &agentmaticv1.ToolCallEvent{
					CallId:    callID,
					ToolName:  toolName,
					Arguments: jsonStringToStruct(step.Action.Arguments),
				},
			},
		})
	}

	if step.Action != nil || step.Observation != "" {
		events = append(events, &agentmaticv1.ExecutionEvent{
			StepIndex:       stepIdx,
			TimestampUnixMs: nowMs,
			Payload: &agentmaticv1.ExecutionEvent_ToolResult{
				ToolResult: &agentmaticv1.ToolResultEvent{
					CallId:   callID,
					ToolName: toolName,
					Result:   observationToValue(step.Observation),
					IsError:  step.IsError,
				},
			},
		})
	}

	return events
}

// finalAnswerEvent builds the terminal FinalAnswer ExecutionEvent with cumulative token metrics.
func finalAnswerEvent(stepIndex int32, res *engine.RunResult, nowMs int64) *agentmaticv1.ExecutionEvent {
	return &agentmaticv1.ExecutionEvent{
		StepIndex:       stepIndex,
		TimestampUnixMs: nowMs,
		Payload: &agentmaticv1.ExecutionEvent_FinalAnswer{
			FinalAnswer: &agentmaticv1.FinalAnswerEvent{
				Answer: res.Answer,
				Usage: &agentmaticv1.UsageMetrics{
					TotalSteps:       int32(len(res.Steps)),
					PromptTokens:     int32(res.Usage.PromptTokens),
					CompletionTokens: int32(res.Usage.CompletionTokens),
					TotalTokens:      int32(res.Usage.TotalTokens),
				},
			},
		},
	}
}

// jsonStringToStruct parses a JSON object string into a protobuf Struct, returning an empty Struct on failure.
func jsonStringToStruct(raw string) *structpb.Struct {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return &structpb.Struct{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil || m == nil {
		return &structpb.Struct{}
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return &structpb.Struct{}
	}
	return s
}

// observationToValue parses structured JSON tool outputs into a protobuf Value, falling back to a string Value for raw text/logs.
func observationToValue(obs string) *structpb.Value {
	trimmed := strings.TrimSpace(obs)
	if trimmed != "" {
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			if val, err := structpb.NewValue(parsed); err == nil {
				return val
			}
		}
	}
	return structpb.NewStringValue(obs)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*connect.Error](err); ok {
		return err
	}

	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, auth.ErrForbidden):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, session.ErrSessionNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, session.ErrDuplicateSessionName):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, session.ErrSessionPaused):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, session.ErrEmptyPrompt):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
