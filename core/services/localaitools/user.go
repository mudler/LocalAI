package localaitools

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/http/auth"
)

type userCtxKey struct{}

// WithUser returns a context that carries the user a tool call acts for.
// The in-process client reads it so that a call is made with the rights of
// that user. A nil user returns ctx unchanged.
func WithUser(ctx context.Context, u *auth.User) context.Context {
	if u == nil {
		return ctx
	}
	return context.WithValue(ctx, userCtxKey{}, u)
}

// UserFromContext returns the user that WithUser stored, or nil.
func UserFromContext(ctx context.Context) *auth.User {
	u, _ := ctx.Value(userCtxKey{}).(*auth.User)
	return u
}

// userTokenMeta is the key, in the _meta of a tool call, of a one-use token
// that names the acting user. The MCP session between the chat and the
// in-process server is a protocol boundary: context values do not cross it,
// so the caller registers the user here and sends only the token.
const userTokenMeta = "localai.io/acting-user"

// actingUsers maps live tokens to users. A token exists only while the call
// that registered it runs, and only a caller in this process can register one,
// so a remote MCP client can neither create nor guess a token.
var actingUsers sync.Map

// ActingUserMeta registers the user of ctx for the duration of one tool call
// and returns the _meta to send with it. Call release when the call is done.
// When ctx carries no user it returns nil and a no-op release.
func ActingUserMeta(ctx context.Context) (meta map[string]any, release func()) {
	u := UserFromContext(ctx)
	if u == nil {
		return nil, func() {}
	}
	token := uuid.NewString()
	actingUsers.Store(token, u)
	return map[string]any{userTokenMeta: token}, func() { actingUsers.Delete(token) }
}

// actingUserMiddleware puts the user named by a call's token into the context
// of the tool handler. An unknown or missing token changes nothing.
func actingUserMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/call" {
			if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && p != nil {
				ctx = userFromToken(ctx, p.GetMeta())
			} else if p, ok := req.GetParams().(*mcp.CallToolParams); ok && p != nil {
				ctx = userFromToken(ctx, p.GetMeta())
			}
		}
		return next(ctx, method, req)
	}
}

func userFromToken(ctx context.Context, meta map[string]any) context.Context {
	token, _ := meta[userTokenMeta].(string)
	if token == "" {
		return ctx
	}
	if v, ok := actingUsers.Load(token); ok {
		return WithUser(ctx, v.(*auth.User))
	}
	return ctx
}
