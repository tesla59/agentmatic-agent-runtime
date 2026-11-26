package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ErrForbidden indicates the caller is not permitted by AgentMatic policy or Kubernetes RBAC.
var ErrForbidden = errors.New("forbidden")

// Authorizer checks whether the caller in ctx is permitted to execute toolName with rawArgs.
type Authorizer interface {
	Authorize(ctx context.Context, toolName string, rawArgs json.RawMessage) error
}

// AllowAllAuthorizer permits all tool executions for standalone local debugging.
type AllowAllAuthorizer struct{}

// NewAllowAllAuthorizer creates a permissive Authorizer for local development mode.
func NewAllowAllAuthorizer() *AllowAllAuthorizer {
	return &AllowAllAuthorizer{}
}

// Authorize allows all tool calls unless ctx has been cancelled.
func (a *AllowAllAuthorizer) Authorize(ctx context.Context, _ string, _ json.RawMessage) error {
	return ctx.Err()
}

type toolPermissionSpec struct {
	group         string
	resource      string
	subresource   string
	verb          string
	namespaced    bool
	nameArgField  string
	allowedPolicy bool
}

var defaultToolPermissions = map[string]toolPermissionSpec{
	"k8s_list_pods": {
		group:         "",
		resource:      "pods",
		verb:          "list",
		namespaced:    true,
		allowedPolicy: true,
	},
	"k8s_get_pod_logs": {
		group:         "",
		resource:      "pods",
		subresource:   "log",
		verb:          "get",
		namespaced:    true,
		nameArgField:  "pod",
		allowedPolicy: true,
	},
	"k8s_get_events": {
		group:         "",
		resource:      "events",
		verb:          "list",
		namespaced:    true,
		allowedPolicy: true,
	},
	"k8s_list_deployments": {
		group:         "apps",
		resource:      "deployments",
		verb:          "list",
		namespaced:    true,
		allowedPolicy: true,
	},
	"k8s_get_deployment": {
		group:         "apps",
		resource:      "deployments",
		verb:          "get",
		namespaced:    true,
		nameArgField:  "name",
		allowedPolicy: true,
	},
	"k8s_list_nodes": {
		group:         "",
		resource:      "nodes",
		verb:          "list",
		namespaced:    false,
		allowedPolicy: true,
	},
	"k8s_list_services": {
		group:         "",
		resource:      "services",
		verb:          "list",
		namespaced:    true,
		allowedPolicy: true,
	},
	"k8s_get_configmap": {
		group:         "",
		resource:      "configmaps",
		verb:          "get",
		namespaced:    true,
		nameArgField:  "name",
		allowedPolicy: true,
	},
}

// SARAuthorizer enforces AgentMatic automation policy and Kubernetes SubjectAccessReview checks before tool execution.
type SARAuthorizer struct {
	client      kubernetes.Interface
	permissions map[string]toolPermissionSpec
}

// NewSARAuthorizer creates an Authorizer backed by Kubernetes SubjectAccessReview.
func NewSARAuthorizer(client kubernetes.Interface) (*SARAuthorizer, error) {
	if client == nil {
		return nil, errors.New("kubernetes client is required")
	}
	return &SARAuthorizer{
		client:      client,
		permissions: defaultToolPermissions,
	}, nil
}

// Authorize validates caller identity in ctx, tool automation policy, and Kubernetes RBAC via SubjectAccessReview.
func (a *SARAuthorizer) Authorize(ctx context.Context, toolName string, rawArgs json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	id, ok := IdentityFromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: verified identity missing from context", ErrUnauthenticated)
	}

	spec, ok := a.permissions[toolName]
	if !ok {
		return fmt.Errorf("%w: tool %q has no registered authorization policy", ErrForbidden, toolName)
	}
	if !spec.allowedPolicy {
		return fmt.Errorf("%w: tool %q is blocked by AgentMatic policy", ErrForbidden, toolName)
	}

	attrs, err := buildResourceAttributes(spec, rawArgs)
	if err != nil {
		return err
	}

	var groups []string
	if len(id.Groups) > 0 {
		groups = append([]string(nil), id.Groups...)
	}

	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:               id.Username,
			UID:                id.UserUID,
			Groups:             groups,
			ResourceAttributes: attrs,
		},
	}

	resp, err := a.client.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("subject access review failed: %w", err)
	}

	if resp.Status.Denied {
		return fmt.Errorf("%w: kubernetes RBAC explicitly denied %s on %s", ErrForbidden, attrs.Verb, formatTarget(attrs))
	}
	if !resp.Status.Allowed {
		reason := resp.Status.Reason
		if reason == "" {
			reason = "not permitted by Kubernetes RBAC"
		}
		return fmt.Errorf("%w: %s for %s on %s", ErrForbidden, reason, attrs.Verb, formatTarget(attrs))
	}

	return nil
}

func buildResourceAttributes(spec toolPermissionSpec, rawArgs json.RawMessage) (*authorizationv1.ResourceAttributes, error) {
	args := make(map[string]any)
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, fmt.Errorf("invalid tool arguments for authorization: %w", err)
		}
	}

	attrs := &authorizationv1.ResourceAttributes{
		Group:       spec.group,
		Version:     "v1",
		Resource:    spec.resource,
		Subresource: spec.subresource,
		Verb:        spec.verb,
	}

	if spec.namespaced {
		ns := "default"
		if rawNs, ok := args["namespace"].(string); ok {
			if trimmed := strings.TrimSpace(rawNs); trimmed != "" {
				ns = trimmed
			}
		}
		attrs.Namespace = ns
	}

	if spec.nameArgField != "" {
		if rawName, ok := args[spec.nameArgField].(string); ok {
			attrs.Name = strings.TrimSpace(rawName)
		}
	}

	return attrs, nil
}

func formatTarget(attrs *authorizationv1.ResourceAttributes) string {
	res := attrs.Resource
	if attrs.Subresource != "" {
		res += "/" + attrs.Subresource
	}
	if attrs.Namespace != "" {
		res += " in namespace " + attrs.Namespace
	}
	return res
}
