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

//go:embed schemas/k8s_list_nodes.json
var listNodesSchemaBytes []byte

var listNodesSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(listNodesSchemaBytes, &s); err != nil {
		panic("invalid list nodes schema: " + err.Error())
	}
	return &s
}()

type ListNodesTool struct {
	client kubernetes.Interface
}

func NewListNodesTool(client kubernetes.Interface) *ListNodesTool {
	return &ListNodesTool{client: client}
}

func (t *ListNodesTool) Name() string {
	return "k8s_list_nodes"
}

func (t *ListNodesTool) Description() string {
	return "List cluster worker and control-plane nodes with their status conditions (Ready, MemoryPressure, DiskPressure, PIDPressure), capacity, allocatable resources, and taints."
}

func (t *ListNodesTool) Parameters() *jsonschema.Schema {
	return listNodesSchema
}

type listNodesInput struct {
	LabelSelector string `json:"label_selector"`
}

func (t *ListNodesTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input listNodesInput
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &input); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}

	nodes, err := t.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: input.LabelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}

	out, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal nodes: %w", err)
	}

	return string(out), nil
}
