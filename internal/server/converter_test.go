package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
	"github.com/tesla59/agentmatic-agent-runtime/internal/tools"
)

type stubLLM struct{}

func (s *stubLLM) Chat(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return &llm.Response{Message: llm.AssistantMessage(llm.TextPart{Text: "done"})}, nil
}

func (s *stubLLM) ChatStream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	return nil, nil
}

func TestExtractTurnsAndSessionConversion(t *testing.T) {
	history := []llm.Message{
		llm.SystemMessage("system prompt"),
		llm.UserMessage("check pods"),
		llm.AssistantMessage(
			llm.ThinkingPart{Thought: "need pods"},
			llm.ToolCallPart{ID: "1", Name: "k8s_list_pods", Arguments: `{}`},
		),
		llm.ToolResultMessage("1", `[{"name":"api"}]`, false),
		llm.AssistantMessage(llm.TextPart{Text: "All pods healthy."}),
		llm.UserMessage("check logs"),
		llm.AssistantMessage(llm.TextPart{Text: "No errors in logs."}),
	}

	turns := extractTurns(history)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].TurnIndex != 0 || turns[0].UserPrompt != "check pods" || turns[0].AssistantResponse != "All pods healthy." {
		t.Errorf("unexpected turn 0: %+v", turns[0])
	}
	if turns[1].TurnIndex != 1 || turns[1].UserPrompt != "check logs" || turns[1].AssistantResponse != "No errors in logs." {
		t.Errorf("unexpected turn 1: %+v", turns[1])
	}

	eng, _ := engine.New(engine.Config{Client: &stubLLM{}, Registry: tools.NewRegistry()})
	owner := session.Identity{
		ClusterID:   "prod",
		UserUID:     "uid-1",
		Username:    "alice",
		Groups:      []string{"sre"},
		DisplayName: "Alice",
		Email:       "alice@example.com",
	}
	sess, err := session.New(session.Config{
		Name:   "pod-health",
		Title:  "Pod Health",
		Owner:  owner,
		Engine: eng,
	})
	if err != nil {
		t.Fatalf("session.New failed: %v", err)
	}

	sum := sessionSummaryToProto(sess)
	if sum.GetName() != "pod-health" || sum.Title != "Pod Health" || sum.Owner.GetEmail() != "alice@example.com" {
		t.Errorf("unexpected summary: %+v", sum)
	}
	det := sessionDetailsToProto(sess)
	if det.Id != sess.ID() || det.Owner.UserUid != "uid-1" {
		t.Errorf("unexpected details: %+v", det)
	}
}

func TestStepToExecutionEvents(t *testing.T) {
	// 1. JSON tool result + JSON object arguments
	stepJSON := engine.Step{
		Number:      1,
		Thinking:    "Listing pods",
		Action:      &llm.ToolCallPart{ID: "c1", Name: "k8s_list_pods", Arguments: `{"namespace":"prod"}`},
		Observation: `{"items":["pod-a"]}`,
		IsError:     false,
	}
	events := stepToExecutionEvents(stepJSON, 1000)
	if len(events) != 3 {
		t.Fatalf("expected 3 events (thinking, call, result), got %d", len(events))
	}
	if events[0].GetThinking().GetThought() != "Listing pods" {
		t.Errorf("unexpected thinking event: %+v", events[0])
	}
	tc := events[1].GetToolCall()
	if tc.GetToolName() != "k8s_list_pods" || tc.GetArguments().AsMap()["namespace"] != "prod" {
		t.Errorf("unexpected tool call event: %+v", tc)
	}
	tr := events[2].GetToolResult()
	if tr.GetIsError() {
		t.Error("expected IsError=false")
	}
	resultMap, ok := tr.GetResult().AsInterface().(map[string]any)
	if !ok || len(resultMap) == 0 {
		t.Errorf("expected structured JSON object result, got %#v", tr.GetResult().AsInterface())
	}

	// 2. Plain text error observation (e.g., pod logs or RBAC denial)
	stepText := engine.Step{
		Number:      2,
		Action:      &llm.ToolCallPart{ID: "c2", Name: "k8s_get_pod_logs", Arguments: `{}`},
		Observation: "Error: forbidden",
		IsError:     true,
	}
	errEvents := stepToExecutionEvents(stepText, 2000)
	if len(errEvents) != 2 {
		t.Fatalf("expected 2 events (call, result), got %d", len(errEvents))
	}
	errRes := errEvents[1].GetToolResult()
	if !errRes.GetIsError() || errRes.GetResult().GetStringValue() != "Error: forbidden" {
		t.Errorf("unexpected error tool result: %+v", errRes)
	}

	// 3. FinalAnswerEvent
	fin := finalAnswerEvent(3, &engine.RunResult{
		Answer: "Resolved",
		Steps:  []engine.Step{stepJSON, stepText},
		Usage:  llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, 3000)
	if fa := fin.GetFinalAnswer(); fa == nil || fa.Answer != "Resolved" || fa.Usage.TotalSteps != 2 || fa.Usage.TotalTokens != 15 {
		t.Errorf("unexpected final answer event: %+v", fin)
	}
}

func TestMapError(t *testing.T) {
	cases := []struct {
		err  error
		want connect.Code
	}{
		{fmt.Errorf("wrap: %w", auth.ErrUnauthenticated), connect.CodeUnauthenticated},
		{fmt.Errorf("wrap: %w", auth.ErrForbidden), connect.CodePermissionDenied},
		{session.ErrSessionNotFound, connect.CodeNotFound},
		{session.ErrDuplicateSessionName, connect.CodeAlreadyExists},
		{session.ErrSessionPaused, connect.CodeFailedPrecondition},
		{session.ErrEmptyPrompt, connect.CodeInvalidArgument},
		{context.Canceled, connect.CodeCanceled},
		{context.DeadlineExceeded, connect.CodeDeadlineExceeded},
		{errors.New("unexpected"), connect.CodeInternal},
	}

	for _, tc := range cases {
		mapped := mapError(tc.err)
		if connect.CodeOf(mapped) != tc.want {
			t.Errorf("mapError(%v): got %v, want %v", tc.err, connect.CodeOf(mapped), tc.want)
		}
	}

	var nilCheck *agentmaticv1.ExecutionEvent
	_ = nilCheck
}
