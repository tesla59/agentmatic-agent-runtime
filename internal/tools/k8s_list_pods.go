package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

//go:embed schemas/k8s_list_pods.json
var listPodsSchemaBytes []byte

var listPodsSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(listPodsSchemaBytes, &s); err != nil {
		panic("invalid list pods schema: " + err.Error())
	}
	return &s
}()

type ListPodsTool struct {
	client kubernetes.Interface
}

func NewListPodsTool(client kubernetes.Interface) *ListPodsTool {
	return &ListPodsTool{client: client}
}

func (t *ListPodsTool) Name() string {
	return "k8s_list_pods"
}

func (t *ListPodsTool) Description() string {
	return "List pods in a Kubernetes namespace."
}

func (t *ListPodsTool) Parameters() *jsonschema.Schema {
	return listPodsSchema
}

type listPodsInput struct {
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"label_selector"`
}

func (t *ListPodsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input listPodsInput
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &input); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if input.Namespace == "" {
		input.Namespace = "default"
	}

	pods, err := t.client.CoreV1().Pods(input.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: input.LabelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods: %w", err)
	}

	out, err := json.MarshalIndent(pods, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal pods: %w", err)
	}

	return string(out), nil
}
