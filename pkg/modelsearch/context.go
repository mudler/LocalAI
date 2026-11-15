package modelsearch

import "context"

type userKey struct{}

// WithUser returns a context that carries the person a search runs for. A
// source that comes from an add-on is called as that person, so the add-on
// gateway applies its own visibility and feature checks.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// UserFrom returns the person of the context, or an empty string.
func UserFrom(ctx context.Context) string {
	u, _ := ctx.Value(userKey{}).(string)
	return u
}
