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

//go:embed schemas/k8s_get_configmap.json
var getConfigMapSchemaBytes []byte

var getConfigMapSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(getConfigMapSchemaBytes, &s); err != nil {
		panic("invalid get configmap schema: " + err.Error())
	}
	return &s
}()

type GetConfigMapTool struct {
	client kubernetes.Interface
}

func NewGetConfigMapTool(client kubernetes.Interface) *GetConfigMapTool {
	return &GetConfigMapTool{client: client}
}

func (t *GetConfigMapTool) Name() string {
	return "k8s_get_configmap"
}

func (t *GetConfigMapTool) Description() string {
	return "Get the configuration data, binary data keys, and metadata of a specific Kubernetes ConfigMap in a namespace."
}

func (t *GetConfigMapTool) Parameters() *jsonschema.Schema {
	return getConfigMapSchema
}

type getConfigMapInput struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func (t *GetConfigMapTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input getConfigMapInput
	if err := json.Unmarshal(rawArgs, &input); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", fmt.Errorf("configmap name is required")
	}

	namespace := strings.TrimSpace(input.Namespace)
	if namespace == "" {
		namespace = "default"
	}

	cm, err := t.client.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get configmap %q in namespace %q: %w", name, namespace, err)
	}

	out, err := json.MarshalIndent(cm, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal configmap: %w", err)
	}

	return string(out), nil
}
