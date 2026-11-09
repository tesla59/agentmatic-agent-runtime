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

//go:embed schemas/k8s_list_services.json
var listServicesSchemaBytes []byte

var listServicesSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(listServicesSchemaBytes, &s); err != nil {
		panic("invalid list services schema: " + err.Error())
	}
	return &s
}()

type ListServicesTool struct {
	client kubernetes.Interface
}

func NewListServicesTool(client kubernetes.Interface) *ListServicesTool {
	return &ListServicesTool{client: client}
}

func (t *ListServicesTool) Name() string {
	return "k8s_list_services"
}

func (t *ListServicesTool) Description() string {
	return "List Kubernetes services in a namespace with their types, cluster IPs, exposed ports, and pod label selectors."
}

func (t *ListServicesTool) Parameters() *jsonschema.Schema {
	return listServicesSchema
}

type listServicesInput struct {
	Namespace     string `json:"namespace"`
	LabelSelector string `json:"label_selector"`
}

func (t *ListServicesTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input listServicesInput
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &input); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if input.Namespace == "" {
		input.Namespace = "default"
	}

	services, err := t.client.CoreV1().Services(input.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: input.LabelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list services: %w", err)
	}

	out, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal services: %w", err)
	}

	return string(out), nil
}
