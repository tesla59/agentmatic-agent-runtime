package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/jsonschema-go/jsonschema"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

//go:embed schemas/k8s_get_pod_logs.json
var getPodLogsSchemaBytes []byte

var getPodLogsSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(getPodLogsSchemaBytes, &s); err != nil {
		panic("invalid get pod logs schema: " + err.Error())
	}
	return &s
}()

type GetPodLogsTool struct {
	client kubernetes.Interface
}

func NewGetPodLogsTool(client kubernetes.Interface) *GetPodLogsTool {
	return &GetPodLogsTool{client: client}
}

func (t *GetPodLogsTool) Name() string {
	return "k8s_get_pod_logs"
}

func (t *GetPodLogsTool) Description() string {
	return "Fetch logs for a pod or container, with support for tail limits and previous crashed instances."
}

func (t *GetPodLogsTool) Parameters() *jsonschema.Schema {
	return getPodLogsSchema
}

type getPodLogsInput struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
	TailLines *int64 `json:"tail_lines,omitempty"`
	Previous  bool   `json:"previous,omitempty"`
}

func (t *GetPodLogsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input getPodLogsInput
	if err := json.Unmarshal(rawArgs, &input); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if input.Pod == "" {
		return "", errors.New("'pod' argument is required")
	}
	if input.Namespace == "" {
		input.Namespace = "default"
	}

	tail := int64(100)
	if input.TailLines != nil && *input.TailLines > 0 {
		tail = *input.TailLines
		if tail > 1000 {
			tail = 1000 // Upper ceiling
		}
	}

	opts := &corev1.PodLogOptions{
		Container: input.Container,
		TailLines: &tail,
		Previous:  input.Previous,
	}

	req := t.client.CoreV1().Pods(input.Namespace).GetLogs(input.Pod, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to open log stream for pod %q: %w", input.Pod, err)
	}
	defer stream.Close()

	logBytes, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}

	if len(logBytes) == 0 {
		return "[no logs found]", nil
	}

	return string(logBytes), nil
}
