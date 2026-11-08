package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

//go:embed schemas/k8s_get_deployment.json
var getDeploymentSchemaBytes []byte

var getDeploymentSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(getDeploymentSchemaBytes, &s); err != nil {
		panic("invalid get deployment schema: " + err.Error())
	}
	return &s
}()

type GetDeploymentTool struct {
	client kubernetes.Interface
}

func NewGetDeploymentTool(client kubernetes.Interface) *GetDeploymentTool {
	return &GetDeploymentTool{client: client}
}

func (t *GetDeploymentTool) Name() string {
	return "k8s_get_deployment"
}

func (t *GetDeploymentTool) Description() string {
	return "Get full specification and detailed status of a specific Kubernetes deployment, including pod template, containers, image tags, probes, and rollout conditions."
}

func (t *GetDeploymentTool) Parameters() *jsonschema.Schema {
	return getDeploymentSchema
}

type getDeploymentInput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func (t *GetDeploymentTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input getDeploymentInput
	if err := json.Unmarshal(rawArgs, &input); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", fmt.Errorf("deployment name is required")
	}

	namespace := strings.TrimSpace(input.Namespace)
	if namespace == "" {
		namespace = "default"
	}

	dep, err := t.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get deployment %q in namespace %q: %w", name, namespace, err)
	}

	out, err := json.MarshalIndent(dep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal deployment: %w", err)
	}

	return string(out), nil
}
