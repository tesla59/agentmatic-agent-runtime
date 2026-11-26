package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

func TestAllowAllAuthorizer(t *testing.T) {
	authz := NewAllowAllAuthorizer()
	if err := authz.Authorize(context.Background(), "k8s_list_pods", nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := authz.Authorize(ctx, "k8s_list_pods", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestSARAuthorizer_ValidationAndFailClosed(t *testing.T) {
	if _, err := NewSARAuthorizer(nil); err == nil {
		t.Fatal("expected error for nil kubernetes client")
	}

	client := fake.NewSimpleClientset()
	authz, err := NewSARAuthorizer(client)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Missing identity fails closed with ErrUnauthenticated
	if err := authz.Authorize(context.Background(), "k8s_list_pods", json.RawMessage(`{}`)); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}

	ctx := WithIdentity(context.Background(), session.Identity{
		ClusterID: "prod",
		UserUID:   "uid-alice",
		Username:  "alice",
	})

	// Unknown tool fails closed with ErrForbidden
	if err := authz.Authorize(ctx, "k8s_unknown_mutation", json.RawMessage(`{}`)); !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for unknown tool, got %v", err)
	}

	// Malformed JSON arguments fail closed
	if err := authz.Authorize(ctx, "k8s_list_pods", json.RawMessage(`{invalid`)); err == nil {
		t.Error("expected error for malformed JSON args")
	}
}

func TestSARAuthorizer_ResourceAttributeMapping(t *testing.T) {
	alice := session.Identity{
		ClusterID: "prod-us",
		UserUID:   "uid-alice-99",
		Username:  "alice@company.com",
		Groups:    []string{"devs", "sre"},
	}
	ctx := WithIdentity(context.Background(), alice)

	tests := []struct {
		name     string
		tool     string
		args     string
		wantAttr authorizationv1.ResourceAttributes
	}{
		{
			name: "pods default namespace",
			tool: "k8s_list_pods",
			args: `{}`,
			wantAttr: authorizationv1.ResourceAttributes{
				Group: "", Version: "v1", Resource: "pods", Verb: "list", Namespace: "default",
			},
		},
		{
			name: "pod logs subresource and pod name",
			tool: "k8s_get_pod_logs",
			args: `{"namespace":"payments","pod":"api-pod-1"}`,
			wantAttr: authorizationv1.ResourceAttributes{
				Group: "", Version: "v1", Resource: "pods", Subresource: "log", Verb: "get", Namespace: "payments", Name: "api-pod-1",
			},
		},
		{
			name: "deployment get with apps group",
			tool: "k8s_get_deployment",
			args: `{"namespace":"prod","name":"checkout"}`,
			wantAttr: authorizationv1.ResourceAttributes{
				Group: "apps", Version: "v1", Resource: "deployments", Verb: "get", Namespace: "prod", Name: "checkout",
			},
		},
		{
			name: "nodes cluster scoped",
			tool: "k8s_list_nodes",
			args: `{"namespace":"ignored"}`,
			wantAttr: authorizationv1.ResourceAttributes{
				Group: "", Version: "v1", Resource: "nodes", Verb: "list", Namespace: "",
			},
		},
		{
			name: "configmap get",
			tool: "k8s_get_configmap",
			args: `{"name":"nginx-conf"}`,
			wantAttr: authorizationv1.ResourceAttributes{
				Group: "", Version: "v1", Resource: "configmaps", Verb: "get", Namespace: "default", Name: "nginx-conf",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			var captured *authorizationv1.SubjectAccessReviewSpec
			client.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
				sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
				captured = sar.Spec.DeepCopy()
				sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: true}
				return true, sar, nil
			})

			authz, _ := NewSARAuthorizer(client)
			if err := authz.Authorize(ctx, tc.tool, json.RawMessage(tc.args)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if captured == nil || captured.ResourceAttributes == nil {
				t.Fatal("expected SubjectAccessReview request with ResourceAttributes")
			}
			if captured.User != alice.Username || captured.UID != alice.UserUID || len(captured.Groups) != 2 {
				t.Errorf("unexpected SAR subject: %+v", captured)
			}
			got := *captured.ResourceAttributes
			if got != tc.wantAttr {
				t.Errorf("got ResourceAttributes %+v, want %+v", got, tc.wantAttr)
			}
		})
	}
}

func TestSARAuthorizer_DenialAndErrors(t *testing.T) {
	ctx := WithIdentity(context.Background(), session.Identity{
		ClusterID: "prod",
		UserUID:   "uid-bob",
		Username:  "bob",
	})

	t.Run("not allowed returns ErrForbidden", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
			sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: false}
			return true, sar, nil
		})

		authz, _ := NewSARAuthorizer(client)
		if err := authz.Authorize(ctx, "k8s_list_pods", json.RawMessage(`{"namespace":"kube-system"}`)); !errors.Is(err, ErrForbidden) {
			t.Errorf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("explicitly denied overrides allowed", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			sar := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
			sar.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: true, Denied: true}
			return true, sar, nil
		})

		authz, _ := NewSARAuthorizer(client)
		if err := authz.Authorize(ctx, "k8s_list_pods", nil); !errors.Is(err, ErrForbidden) {
			t.Errorf("expected ErrForbidden when Denied=true, got %v", err)
		}
	})

	t.Run("API error fails closed without ErrForbidden", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("create", "subjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("sar api error")
		})

		authz, _ := NewSARAuthorizer(client)
		err := authz.Authorize(ctx, "k8s_list_pods", nil)
		if err == nil {
			t.Fatal("expected error when SAR call fails")
		}
		if errors.Is(err, ErrForbidden) {
			t.Errorf("expected infrastructure error distinct from ErrForbidden, got %v", err)
		}
	})
}
