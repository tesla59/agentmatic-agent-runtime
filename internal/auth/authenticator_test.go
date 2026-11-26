package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

func TestIdentityContext(t *testing.T) {
	if _, ok := IdentityFromContext(context.Background()); ok {
		t.Error("expected false on context without identity")
	}

	// Invalid identity (missing UID) must not be accepted from context
	invalidCtx := WithIdentity(context.Background(), session.Identity{ClusterID: "prod"})
	if _, ok := IdentityFromContext(invalidCtx); ok {
		t.Error("expected false when stored identity is invalid")
	}

	orig := session.Identity{
		ClusterID: "prod-us",
		UserUID:   "uid-alice",
		Username:  "alice@company.com",
		Groups:    []string{"sre", "devs"},
	}
	ctx := WithIdentity(context.Background(), orig)

	// Mutating original slice must not corrupt context identity
	orig.Groups[0] = "tampered"

	got, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("expected identity from context")
	}
	if !got.SameOwner(orig) {
		t.Errorf("expected same owner, got %+v", got)
	}
	if got.Groups[0] != "sre" {
		t.Errorf("expected defensive copy of groups, got %v", got.Groups)
	}

	// Mutating retrieved copy must not affect future lookups
	got.Groups[0] = "tampered-again"
	got2, _ := IdentityFromContext(ctx)
	if got2.Groups[0] != "sre" {
		t.Errorf("expected immutable context identity, got %v", got2.Groups)
	}
}

func TestLocalDevAuthenticator(t *testing.T) {
	authn := NewLocalDevAuthenticator()
	id, err := authn.Authenticate(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !id.SameOwner(session.LocalDevIdentity()) {
		t.Errorf("expected LocalDevIdentity, got %+v", id)
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := authn.Authenticate(cancelledCtx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestNewTokenReviewAuthenticator_Validation(t *testing.T) {
	if _, err := NewTokenReviewAuthenticator(TokenReviewConfig{ClusterID: "prod"}); err == nil {
		t.Error("expected error when client is nil")
	}
	if _, err := NewTokenReviewAuthenticator(TokenReviewConfig{Client: fake.NewSimpleClientset()}); err == nil {
		t.Error("expected error when clusterID is empty")
	}
}

func TestTokenReviewAuthenticator_HeaderErrors(t *testing.T) {
	authn, err := NewTokenReviewAuthenticator(TokenReviewConfig{
		Client:    fake.NewSimpleClientset(),
		ClusterID: "prod-us",
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	cases := []struct {
		name    string
		headers http.Header
	}{
		{name: "nil headers", headers: nil},
		{name: "missing header", headers: http.Header{}},
		{name: "non-bearer scheme", headers: http.Header{"Authorization": []string{"Basic dXNlcjpwYXNz"}}},
		{name: "empty bearer token", headers: http.Header{"Authorization": []string{"Bearer    "}}},
		{name: "whitespace inside token", headers: http.Header{"Authorization": []string{"Bearer abc def"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := authn.Authenticate(context.Background(), tc.headers)
			if !errors.Is(err, ErrUnauthenticated) {
				t.Errorf("expected ErrUnauthenticated, got %v", err)
			}
		})
	}
}

func TestTokenReviewAuthenticator_Authenticate(t *testing.T) {
	secretToken := "super-secret-k8s-bearer-token"

	t.Run("valid token and audience forwarding", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		var capturedReview *authenticationv1.TokenReview
		client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			tr := action.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
			capturedReview = tr.DeepCopy()
			tr.Status = authenticationv1.TokenReviewStatus{
				Authenticated: true,
				User: authenticationv1.UserInfo{
					Username: "alice@company.com",
					UID:      "uid-alice-123",
					Groups:   []string{"system:authenticated", "sre"},
				},
			}
			return true, tr, nil
		})

		authn, err := NewTokenReviewAuthenticator(TokenReviewConfig{
			Client:    client,
			ClusterID: "prod-us-east",
			Audiences: []string{"agentmatic"},
		})
		if err != nil {
			t.Fatalf("setup failed: %v", err)
		}

		headers := http.Header{}
		headers.Set("Authorization", "bearer "+secretToken) // test case-insensitive scheme

		id, err := authn.Authenticate(context.Background(), headers)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedReview == nil || capturedReview.Spec.Token != secretToken {
			t.Fatalf("expected TokenReview spec token to match")
		}
		if len(capturedReview.Spec.Audiences) != 1 || capturedReview.Spec.Audiences[0] != "agentmatic" {
			t.Errorf("expected audiences forwarded, got %v", capturedReview.Spec.Audiences)
		}
		if id.ClusterID != "prod-us-east" || id.UserUID != "uid-alice-123" || id.Username != "alice@company.com" {
			t.Errorf("unexpected identity: %+v", id)
		}
		if len(id.Groups) != 2 || id.Groups[1] != "sre" {
			t.Errorf("unexpected groups: %v", id.Groups)
		}
	})

	t.Run("rejected token does not leak secret", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			tr := action.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
			tr.Status = authenticationv1.TokenReviewStatus{
				Authenticated: false,
				Error:         "token expired",
			}
			return true, tr, nil
		})

		authn, _ := NewTokenReviewAuthenticator(TokenReviewConfig{Client: client, ClusterID: "prod"})
		headers := http.Header{"Authorization": []string{"Bearer " + secretToken}}

		_, err := authn.Authenticate(context.Background(), headers)
		if !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("expected ErrUnauthenticated, got %v", err)
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Errorf("security violation: error leaked raw token: %v", err)
		}
	})

	t.Run("missing UID fails closed", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			tr := action.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
			tr.Status = authenticationv1.TokenReviewStatus{
				Authenticated: true,
				User: authenticationv1.UserInfo{
					Username: "alice",
					UID:      "",
				},
			}
			return true, tr, nil
		})

		authn, _ := NewTokenReviewAuthenticator(TokenReviewConfig{Client: client, ClusterID: "prod"})
		headers := http.Header{"Authorization": []string{"Bearer " + secretToken}}

		if _, err := authn.Authenticate(context.Background(), headers); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("expected ErrUnauthenticated when UID is empty, got %v", err)
		}
	})

	t.Run("k8s API error is distinct from ErrUnauthenticated", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("apiserver unreachable")
		})

		authn, _ := NewTokenReviewAuthenticator(TokenReviewConfig{Client: client, ClusterID: "prod"})
		headers := http.Header{"Authorization": []string{"Bearer " + secretToken}}

		_, err := authn.Authenticate(context.Background(), headers)
		if err == nil {
			t.Fatal("expected error when API fails")
		}
		if errors.Is(err, ErrUnauthenticated) {
			t.Errorf("expected API failure not to wrap ErrUnauthenticated, got %v", err)
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Errorf("security violation: error leaked raw token: %v", err)
		}
	})
}
