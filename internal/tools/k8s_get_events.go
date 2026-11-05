package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

//go:embed schemas/k8s_get_events.json
var getEventsSchemaBytes []byte

var getEventsSchema = func() *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal(getEventsSchemaBytes, &s); err != nil {
		panic("invalid get events schema: " + err.Error())
	}
	return &s
}()

type GetEventsTool struct {
	client kubernetes.Interface
}

func NewGetEventsTool(client kubernetes.Interface) *GetEventsTool {
	return &GetEventsTool{client: client}
}

func (t *GetEventsTool) Name() string {
	return "k8s_get_events"
}

func (t *GetEventsTool) Description() string {
	return "Retrieve cluster events (warnings, errors, scheduling failures) in a namespace."
}

func (t *GetEventsTool) Parameters() *jsonschema.Schema {
	return getEventsSchema
}

type getEventsInput struct {
	Namespace          string `json:"namespace"`
	InvolvedObjectName string `json:"involved_object_name,omitempty"`
	FullOutput         bool   `json:"full_output,omitempty"`
}

type eventSummary struct {
	Type           string `json:"type"` // Normal or Warning
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	InvolvedObject string `json:"involved_object"`
	Count          int32  `json:"count"`
	LastSeen       string `json:"last_seen"`
}

func (t *GetEventsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (string, error) {
	var input getEventsInput
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &input); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if input.Namespace == "" {
		input.Namespace = "default"
	}

	events, err := t.client.CoreV1().Events(input.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to fetch events in namespace %q: %w", input.Namespace, err)
	}

	// If FullOutput requested, return full k8s event list
	if input.FullOutput {
		out, err := json.MarshalIndent(events, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to format events: %w", err)
		}
		return string(out), nil
	}

	// Otherwise, return compact summaries
	summaries := make([]eventSummary, 0, len(events.Items))
	for _, e := range events.Items {
		if input.InvolvedObjectName != "" && e.InvolvedObject.Name != input.InvolvedObjectName {
			continue
		}
		summaries = append(summaries, summarizeEvent(e))
	}

	if len(summaries) == 0 {
		return "[]", nil
	}

	out, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format event summaries: %w", err)
	}

	return string(out), nil
}

func summarizeEvent(e corev1.Event) eventSummary {
	lastSeen := "unknown"
	if !e.LastTimestamp.IsZero() {
		lastSeen = time.Since(e.LastTimestamp.Time).Round(time.Second).String()
	}

	return eventSummary{
		Type:           e.Type,
		Reason:         e.Reason,
		Message:        e.Message,
		InvolvedObject: fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
		Count:          e.Count,
		LastSeen:       lastSeen,
	}
}
