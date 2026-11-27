package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	agentmaticv1 "github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1"
	"github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1/agentmaticv1connect"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/server"
)

type remoteClient struct {
	sessions agentmaticv1connect.SessionServiceClient
	runtime  agentmaticv1connect.RuntimeServiceClient
	token    string
}

func newRemoteClient(baseURL, token string, httpClient connect.HTTPClient) *remoteClient {
	if httpClient == nil {
		httpClient = defaultRemoteHTTPClient()
	}
	return &remoteClient{
		sessions: agentmaticv1connect.NewSessionServiceClient(httpClient, baseURL),
		runtime:  agentmaticv1connect.NewRuntimeServiceClient(httpClient, baseURL),
		token:    strings.TrimSpace(token),
	}
}

func defaultRemoteHTTPClient() *http.Client {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Client{
		Transport: &http.Transport{
			Protocols: protocols,
		},
	}
}

func (c *remoteClient) withAuth(header http.Header) {
	if c.token != "" {
		header.Set("Authorization", "Bearer "+c.token)
	}
}

func (c *remoteClient) CreateSession(ctx context.Context, name, title string) (SessionInfo, error) {
	msg := &agentmaticv1.CreateSessionRequest{}
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		msg.Name = &trimmed
	}
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		msg.Title = &trimmed
	}

	req := connect.NewRequest(msg)
	c.withAuth(req.Header())

	resp, err := c.sessions.CreateSession(ctx, req)
	if err != nil {
		return SessionInfo{}, formatRemoteError(err)
	}
	return sessionInfoFromDetails(resp.Msg), nil
}

func (c *remoteClient) GetSession(ctx context.Context, identifier string) (SessionInfo, error) {
	req := connect.NewRequest(&agentmaticv1.GetSessionRequest{
		Identifier: &agentmaticv1.GetSessionRequest_Id{Id: strings.TrimSpace(identifier)},
	})
	c.withAuth(req.Header())

	resp, err := c.sessions.GetSession(ctx, req)
	if err == nil {
		return sessionInfoFromDetails(resp.Msg), nil
	}

	// Server's Resolve checks UUID and exact slug name on GetSessionRequest; try Name if Id lookup was not found.
	if connect.CodeOf(err) == connect.CodeNotFound {
		byName := connect.NewRequest(&agentmaticv1.GetSessionRequest{
			Identifier: &agentmaticv1.GetSessionRequest_Name{Name: strings.TrimSpace(identifier)},
		})
		c.withAuth(byName.Header())
		if resp2, err2 := c.sessions.GetSession(ctx, byName); err2 == nil {
			return sessionInfoFromDetails(resp2.Msg), nil
		}
	}

	return SessionInfo{}, formatRemoteError(err)
}

func (c *remoteClient) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	req := connect.NewRequest(&agentmaticv1.ListSessionsRequest{})
	c.withAuth(req.Header())

	resp, err := c.sessions.ListSessions(ctx, req)
	if err != nil {
		return nil, formatRemoteError(err)
	}

	out := make([]SessionInfo, 0, len(resp.Msg.GetSessions()))
	for _, s := range resp.Msg.GetSessions() {
		out = append(out, sessionInfoFromSummary(s))
	}
	return out, nil
}

func (c *remoteClient) RenameSession(ctx context.Context, identifier, newTitle, newName string) (SessionInfo, error) {
	msg := &agentmaticv1.RenameSessionRequest{
		Identifier: &agentmaticv1.RenameSessionRequest_Id{Id: strings.TrimSpace(identifier)},
	}
	if t := strings.TrimSpace(newTitle); t != "" {
		msg.NewTitle = &t
	}
	if n := strings.TrimSpace(newName); n != "" {
		msg.NewName = &n
	}

	req := connect.NewRequest(msg)
	c.withAuth(req.Header())

	resp, err := c.sessions.RenameSession(ctx, req)
	if err != nil {
		return SessionInfo{}, formatRemoteError(err)
	}
	return sessionInfoFromDetails(resp.Msg), nil
}

func (c *remoteClient) RestartSession(ctx context.Context, identifier string) (SessionInfo, error) {
	req := connect.NewRequest(&agentmaticv1.RestartSessionRequest{
		Identifier: &agentmaticv1.RestartSessionRequest_Id{Id: strings.TrimSpace(identifier)},
	})
	c.withAuth(req.Header())

	resp, err := c.sessions.RestartSession(ctx, req)
	if err != nil {
		return SessionInfo{}, formatRemoteError(err)
	}
	return sessionInfoFromDetails(resp.Msg), nil
}

func (c *remoteClient) Execute(ctx context.Context, sessionID, prompt string, cb StreamCallbacks) (ExecuteOutcome, error) {
	msg := &agentmaticv1.ExecuteRequest{
		Prompt: prompt,
	}
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		msg.SessionTarget = &agentmaticv1.ExecuteRequest_SessionId{SessionId: trimmed}
	}

	req := connect.NewRequest(msg)
	c.withAuth(req.Header())

	stream, err := c.runtime.Execute(ctx, req)
	if err != nil {
		return ExecuteOutcome{}, formatRemoteError(err)
	}
	defer func() { _ = stream.Close() }()

	outcome := ExecuteOutcome{SessionID: sessionID}
	for stream.Receive() {
		if headerID := stream.ResponseHeader().Get(server.SessionHeaderKey); headerID != "" {
			outcome.SessionID = headerID
		}

		ev := stream.Msg()
		switch payload := ev.Payload.(type) {
		case *agentmaticv1.ExecutionEvent_Thinking:
			if cb.OnThinking != nil {
				cb.OnThinking(ev.StepIndex, payload.Thinking.GetThought())
			}
		case *agentmaticv1.ExecutionEvent_ToolCall:
			if cb.OnToolCall != nil {
				argsBytes, _ := json.Marshal(payload.ToolCall.GetArguments().AsMap())
				cb.OnToolCall(ev.StepIndex, payload.ToolCall.GetToolName(), string(argsBytes))
			}
		case *agentmaticv1.ExecutionEvent_ToolResult:
			if cb.OnToolResult != nil {
				resVal := formatProtoValue(payload.ToolResult.GetResult().AsInterface())
				cb.OnToolResult(ev.StepIndex, payload.ToolResult.GetToolName(), resVal, payload.ToolResult.GetIsError())
			}
		case *agentmaticv1.ExecutionEvent_FinalAnswer:
			fa := payload.FinalAnswer
			outcome.Answer = fa.GetAnswer()
			if u := fa.GetUsage(); u != nil {
				outcome.Steps = int(u.TotalSteps)
				outcome.Usage = llm.Usage{
					PromptTokens:     int(u.PromptTokens),
					CompletionTokens: int(u.CompletionTokens),
					TotalTokens:      int(u.TotalTokens),
				}
			}
		case *agentmaticv1.ExecutionEvent_Error:
			return outcome, errors.New(payload.Error.GetMessage())
		}
	}

	if headerID := stream.ResponseHeader().Get(server.SessionHeaderKey); headerID != "" {
		outcome.SessionID = headerID
	}
	if err := stream.Err(); err != nil {
		return outcome, formatRemoteError(err)
	}

	return outcome, nil
}

func (c *remoteClient) CancelExecution(ctx context.Context, sessionID string) error {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil
	}
	req := connect.NewRequest(&agentmaticv1.CancelExecutionRequest{
		Identifier: &agentmaticv1.CancelExecutionRequest_Id{Id: trimmed},
	})
	c.withAuth(req.Header())

	_, err := c.runtime.CancelExecution(ctx, req)
	return formatRemoteError(err)
}

func sessionInfoFromSummary(s *agentmaticv1.SessionSummary) SessionInfo {
	return SessionInfo{
		ID:         s.GetId(),
		Name:       s.GetName(),
		Title:      s.GetTitle(),
		Status:     s.GetStatus(),
		OwnerLabel: formatOwnerLabel(s.GetOwner()),
		TurnCount:  int(s.GetTurnCount()),
		UpdatedAt:  time.UnixMilli(s.GetUpdatedAtUnixMs()),
	}
}

func sessionInfoFromDetails(d *agentmaticv1.SessionDetails) SessionInfo {
	return SessionInfo{
		ID:         d.GetId(),
		Name:       d.GetName(),
		Title:      d.GetTitle(),
		Status:     d.GetStatus(),
		OwnerLabel: formatOwnerLabel(d.GetOwner()),
		TurnCount:  int(d.GetTurnCount()),
		UpdatedAt:  time.UnixMilli(d.GetUpdatedAtUnixMs()),
	}
}

func formatOwnerLabel(owner *agentmaticv1.UserIdentity) string {
	if owner == nil {
		return ""
	}
	return fmt.Sprintf("%s (%s/%s)", owner.GetUsername(), owner.GetClusterId(), owner.GetUserUid())
}

func formatProtoValue(val any) string {
	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	b, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", val)
	}
	return string(b)
}

func formatRemoteError(err error) error {
	if err == nil {
		return nil
	}
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		return fmt.Errorf("%w (hint: pass --token / AGENTMATIC_TOKEN or use a kubeconfig context that provides a bearer or exec token)", err)
	}
	return err
}
