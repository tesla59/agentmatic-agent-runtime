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

//go:embed schemas/k8s_list_deployments.json
var listDeploymentsSchemaBytes []byte

var listDeploymentsSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(listDeploymentsSchemaBytes, &s); err != nil {
		panic("invalid list deployments schema: " + err.Error())
	}
	return &s
}()

type ListDeploymentsTool struct {
	client kubernetes.Interface
}

func NewListDeploymentsTool(client kubernetes.Interface) *ListDeploymentsTool {
	return &ListDeploymentsTool{client: client}
}

func (t *ListDeploymentsTool) Name() string {
	return "k8s_list_deployments"
}

func (t *ListDeploymentsTool) Description() string {
	return "List Kubernetes deployments in a namespace with their desired, updated, ready, and available replica counts."
}

func (t *ListDeploymentsTool) Parameters() *jsonschema.Schema {
	return listDeploymentsSchema
}

type listDeploymentsInput struct {
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"label_selector"`
}

func (t *ListDeploymentsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input listDeploymentsInput
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &input); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if input.Namespace == "" {
		input.Namespace = "default"
	}

	deps, err := t.client.AppsV1().Deployments(input.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: input.LabelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list deployments: %w", err)
	}

	out, err := json.MarshalIndent(deps, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal deployments: %w", err)
	}

	return string(out), nil
}
