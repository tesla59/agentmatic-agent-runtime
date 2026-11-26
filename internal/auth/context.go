package auth

import (
	"context"

	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

type identityContextKey struct{}

// WithIdentity returns a child context carrying a defensive copy of the verified session owner identity.
func WithIdentity(ctx context.Context, id session.Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, cloneIdentity(id))
}

// IdentityFromContext extracts the verified session owner identity from ctx.
// Returns false if no valid identity is attached.
func IdentityFromContext(ctx context.Context) (session.Identity, bool) {
	if ctx == nil {
		return session.Identity{}, false
	}
	id, ok := ctx.Value(identityContextKey{}).(session.Identity)
	if !ok || !id.Valid() {
		return session.Identity{}, false
	}
	return cloneIdentity(id), true
}

func cloneIdentity(id session.Identity) session.Identity {
	cloned := id
	if len(id.Groups) > 0 {
		cloned.Groups = make([]string, len(id.Groups))
		copy(cloned.Groups, id.Groups)
	}
	return cloned
}
