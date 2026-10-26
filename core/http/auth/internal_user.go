package auth

import "context"

// UsageSourceChats marks requests that the chat loop made in process, as a
// user it had already authenticated.
const UsageSourceChats = "chats"

// internalUserKey is unexported, so only this package can read or write the
// value. A network request has no Go context, so it cannot carry one.
type internalUserKey struct{}

// WithInternalUser returns a context that makes Middleware accept a request as
// u without a credential. It is for code in this process that already
// authenticated u (the chat loop calling the chat route for its own user). It
// replaces authentication only: the feature gate, model allowlist, quota and
// every handler check still run for u.
func WithInternalUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, internalUserKey{}, u)
}

// InternalUser returns the user an in-process request carries, for a handler or
// test that needs to know who the loop is acting for. It cannot be used to
// forge one: only WithInternalUser writes the value.
func InternalUser(ctx context.Context) (*User, bool) { return internalUser(ctx) }

// internalUser returns the user a request carries in its context. A user who is
// not active is not trusted: the request then goes through normal authentication
// and, with no credential, is rejected.
func internalUser(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(internalUserKey{}).(*User)
	if !ok || u == nil || u.Status != StatusActive {
		return nil, false
	}
	return u, true
}
