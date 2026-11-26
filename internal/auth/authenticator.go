package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

// ErrUnauthenticated indicates missing, malformed, or rejected caller credentials.
var ErrUnauthenticated = errors.New("unauthenticated")

// Authenticator verifies caller credentials from transport headers and returns a trusted identity.
type Authenticator interface {
	Authenticate(ctx context.Context, headers http.Header) (session.Identity, error)
}

// LocalDevAuthenticator returns a static local identity for standalone debugging.
type LocalDevAuthenticator struct {
	identity session.Identity
}

// NewLocalDevAuthenticator creates an Authenticator for local development mode.
func NewLocalDevAuthenticator() *LocalDevAuthenticator {
	return &LocalDevAuthenticator{
		identity: session.LocalDevIdentity(),
	}
}

// Authenticate returns the default local developer identity without checking cluster tokens.
func (a *LocalDevAuthenticator) Authenticate(ctx context.Context, _ http.Header) (session.Identity, error) {
	if err := ctx.Err(); err != nil {
		return session.Identity{}, err
	}
	return cloneIdentity(a.identity), nil
}

// TokenReviewConfig configures a Kubernetes TokenReview authenticator.
type TokenReviewConfig struct {
	Client    kubernetes.Interface
	ClusterID string
	Audiences []string
}

// TokenReviewAuthenticator validates incoming bearer tokens via the Kubernetes TokenReview API.
type TokenReviewAuthenticator struct {
	client    kubernetes.Interface
	clusterID string
	audiences []string
}

// NewTokenReviewAuthenticator initializes a TokenReviewAuthenticator.
func NewTokenReviewAuthenticator(cfg TokenReviewConfig) (*TokenReviewAuthenticator, error) {
	if cfg.Client == nil {
		return nil, errors.New("kubernetes client is required")
	}
	clusterID := strings.TrimSpace(cfg.ClusterID)
	if clusterID == "" {
		return nil, errors.New("cluster ID is required")
	}

	var audiences []string
	if len(cfg.Audiences) > 0 {
		audiences = make([]string, len(cfg.Audiences))
		copy(audiences, cfg.Audiences)
	}

	return &TokenReviewAuthenticator{
		client:    cfg.Client,
		clusterID: clusterID,
		audiences: audiences,
	}, nil
}

// Authenticate validates the Authorization bearer token via Kubernetes TokenReview and returns the verified identity.
func (a *TokenReviewAuthenticator) Authenticate(ctx context.Context, headers http.Header) (session.Identity, error) {
	if err := ctx.Err(); err != nil {
		return session.Identity{}, err
	}

	token, err := extractBearerToken(headers)
	if err != nil {
		return session.Identity{}, err
	}

	spec := authenticationv1.TokenReviewSpec{
		Token: token,
	}
	if len(a.audiences) > 0 {
		spec.Audiences = append([]string(nil), a.audiences...)
	}

	review, err := a.client.AuthenticationV1().TokenReviews().Create(
		ctx,
		&authenticationv1.TokenReview{Spec: spec},
		metav1.CreateOptions{},
	)
	if err != nil {
		// Propagate API/transport errors distinct from ErrUnauthenticated without exposing the token.
		return session.Identity{}, fmt.Errorf("token review request failed: %w", err)
	}

	if !review.Status.Authenticated {
		if review.Status.Error != "" {
			return session.Identity{}, fmt.Errorf("%w: %s", ErrUnauthenticated, review.Status.Error)
		}
		return session.Identity{}, fmt.Errorf("%w: token rejected", ErrUnauthenticated)
	}

	uid := strings.TrimSpace(review.Status.User.UID)
	if uid == "" {
		return session.Identity{}, fmt.Errorf("%w: verified user UID is required", ErrUnauthenticated)
	}

	var groups []string
	if len(review.Status.User.Groups) > 0 {
		groups = make([]string, len(review.Status.User.Groups))
		copy(groups, review.Status.User.Groups)
	}

	return session.Identity{
		ClusterID: a.clusterID,
		UserUID:   uid,
		Username:  review.Status.User.Username,
		Groups:    groups,
	}, nil
}

func extractBearerToken(headers http.Header) (string, error) {
	if headers == nil {
		return "", fmt.Errorf("%w: missing authorization header", ErrUnauthenticated)
	}
	raw := strings.TrimSpace(headers.Get("Authorization"))
	if raw == "" {
		return "", fmt.Errorf("%w: missing authorization header", ErrUnauthenticated)
	}

	scheme, token, found := strings.Cut(raw, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", fmt.Errorf("%w: expected bearer authorization scheme", ErrUnauthenticated)
	}

	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", fmt.Errorf("%w: invalid bearer token format", ErrUnauthenticated)
	}

	return token, nil
}
