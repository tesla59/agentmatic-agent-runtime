package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/tools"
)

const defaultSystemPrompt = "You are AgentMatic, an autonomous Kubernetes DevOps and infrastructure engineer.\n" +
	"Investigate issues methodically using your available tools.\n\n" +
	"Operational Rules:\n" +
	"1. Before invoking any tool, explain your reasoning: state your hypothesis, what data you need, and why you are calling this tool.\n" +
	"2. After receiving an observation, analyze the findings before taking further actions.\n" +
	"3. Provide clear root-cause analysis and actionable recommendations."

// Step captures an individual reasoning and action cycle of the agent.
type Step struct {
	Number      int               `json:"number"`
	Thinking    string            `json:"thinking,omitempty"`
	Message     string            `json:"message,omitempty"`
	Action      *llm.ToolCallPart `json:"action,omitempty"`
	Observation string            `json:"observation,omitempty"`
}

// RunResult contains the complete outcome of a task execution.
type RunResult struct {
	Answer  string        `json:"answer"`
	Steps   []Step        `json:"steps"`
	History []llm.Message `json:"history"`
	Usage   llm.Usage     `json:"usage"`
}

// StepListener receives real-time notifications after each tool execution step.
type StepListener func(step Step)

// Config holds configuration options for initializing the Engine.
type Config struct {
	Client       llm.Client
	Registry     *tools.Registry
	MaxSteps     int          // Defaults to 8 if <= 0
	SystemPrompt string       // Defaults to defaultSystemPrompt if empty
	OnStep       StepListener // Optional callback for progress updates
}

// Engine orchestrates the reasoning and tool execution loop.
type Engine struct {
	client       llm.Client
	registry     *tools.Registry
	maxSteps     int
	systemPrompt string
	onStep       StepListener
}

// New creates a new agent Engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("llm client is required")
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("tool registry is required")
	}

	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}

	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = defaultSystemPrompt
	}

	return &Engine{
		client:       cfg.Client,
		registry:     cfg.Registry,
		maxSteps:     maxSteps,
		systemPrompt: sysPrompt,
		onStep:       cfg.OnStep,
	}, nil
}

// SystemPrompt returns the engine's configured system prompt.
func (e *Engine) SystemPrompt() string {
	return e.systemPrompt
}

// Run executes the agent loop starting with a fresh conversation for the given task.
func (e *Engine) Run(ctx context.Context, task string) (*RunResult, error) {
	history := []llm.Message{
		llm.SystemMessage(e.systemPrompt),
		llm.UserMessage(task),
	}
	return e.RunWithHistory(ctx, history)
}

// RunWithHistory executes the agent loop continuing from an existing conversation history.
func (e *Engine) RunWithHistory(ctx context.Context, history []llm.Message) (*RunResult, error) {
	// Prepare tool definitions from the registry
	var toolDefs []llm.ToolDefinition
	for _, t := range e.registry.List() {
		toolDefs = append(toolDefs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}

	var steps []Step
	var totalUsage llm.Usage

	// Make a shallow copy of history so internal appends do not mutate the caller's slice header
	hist := make([]llm.Message, len(history))
	copy(hist, history)

	for stepNum := 1; stepNum <= e.maxSteps; stepNum++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		resp, err := e.client.Chat(ctx, llm.Request{
			Messages: hist,
			Tools:    toolDefs,
		})
		if err != nil {
			return nil, fmt.Errorf("step %d LLM call failed: %w", stepNum, err)
		}

		// Accumulate token usage
		totalUsage.PromptTokens += resp.Usage.PromptTokens
		totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens

		hist = append(hist, resp.Message)
		toolCalls := resp.Message.ToolCalls()

		// If no tool calls, the model reached its final resolution
		if len(toolCalls) == 0 {
			return &RunResult{
				Answer:  resp.Message.Text(),
				Steps:   steps,
				History: hist,
				Usage:   totalUsage,
			}, nil
		}

		// Execute tool calls requested by the model
		for _, tc := range toolCalls {
			tool, ok := e.registry.Get(tc.Name)
			var observation string
			var isError bool

			if !ok {
				observation = fmt.Sprintf("Error: tool %q not found in registry", tc.Name)
				isError = true
			} else {
				output, execErr := tool.Execute(ctx, json.RawMessage(tc.Arguments))
				if execErr != nil {
					observation = fmt.Sprintf("Error executing %s: %v\nOutput: %s", tc.Name, execErr, output)
					isError = true
				} else {
					observation = output
				}
			}

			// Append observation to context for the model's next turn
			hist = append(hist, llm.ToolResultMessage(tc.ID, observation, isError))

			stepRecord := Step{
				Number:      stepNum,
				Thinking:    resp.Message.Thinking(),
				Message:     resp.Message.Text(),
				Action:      &tc,
				Observation: observation,
			}
			steps = append(steps, stepRecord)

			if e.onStep != nil {
				e.onStep(stepRecord)
			}
		}
	}

	return nil, fmt.Errorf("agent exceeded max steps (%d) without reaching final answer", e.maxSteps)
}
