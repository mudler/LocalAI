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

// voiceGrant is what the host lets one voice_profile call do. The model never
// holds it: only code of this process that has asked a person puts it in a
// context.
type voiceGrant struct {
	// consent: a person confirmed that the speaker agreed to the cloning.
	consent bool
	// checkOnly: validate a create call and send nothing; the host does it to
	// reject a bad clip before it asks the person.
	checkOnly bool
	// pinSHA256 and pinBytes: the clip the person confirmed. A create call
	// that reads another clip is refused (WithVoiceClipPin).
	pinSHA256 string
	pinBytes  int64
}

type voiceGrantKey struct{}

// WithVoiceConsent returns a context in which voice_profile may create a
// profile. Call it only after a person confirmed, in a chat they watch, that
// the speaker agreed to the cloning of their voice.
func WithVoiceConsent(ctx context.Context) context.Context {
	g := voiceGrantFrom(ctx)
	g.consent = true
	return context.WithValue(ctx, voiceGrantKey{}, g)
}

// WithVoiceCheck returns a context in which a voice_profile create call
// validates its input and the clip and then stops, creating nothing.
func WithVoiceCheck(ctx context.Context) context.Context {
	g := voiceGrantFrom(ctx)
	g.checkOnly = true
	return context.WithValue(ctx, voiceGrantKey{}, g)
}

// WithVoiceClipPin returns a context in which a voice_profile create call
// accepts only the clip with this SHA-256 (lowercase hex) and byte size: the
// clip the person saw when they confirmed. A clip that changed since is refused.
func WithVoiceClipPin(ctx context.Context, sha256Hex string, size int64) context.Context {
	g := voiceGrantFrom(ctx)
	g.pinSHA256, g.pinBytes = sha256Hex, size
	return context.WithValue(ctx, voiceGrantKey{}, g)
}

// VoiceGrants reports what the host let this call do: the consent of a person,
// and check-only mode. A stand-in for the tools in a test reads it to act as
// the real tool does.
func VoiceGrants(ctx context.Context) (consent, checkOnly bool) {
	g := voiceGrantFrom(ctx)
	return g.consent, g.checkOnly
}

// VoiceClipPin reports the clip pinned by WithVoiceClipPin, if any. A stand-in
// for the tools in a test reads it to act as the real tool does.
func VoiceClipPin(ctx context.Context) (sha256Hex string, size int64) {
	g := voiceGrantFrom(ctx)
	return g.pinSHA256, g.pinBytes
}

func voiceGrantFrom(ctx context.Context) voiceGrant {
	g, _ := ctx.Value(voiceGrantKey{}).(voiceGrant)
	return g
}

// actingEntry is what a token stands for.
type actingEntry struct {
	user  *auth.User
	grant voiceGrant
	// files is the file store the caller fenced the call to (WithFiles), or nil.
	files FileStore
}

// actingUsers maps live tokens to the user and grants of a call. A token exists only while the call
// that registered it runs, and only a caller in this process can register one,
// so a remote MCP client can neither create nor guess a token.
var actingUsers sync.Map

// ActingUserMeta registers the user of ctx, its voice grants and its file store (WithFiles), for
// the duration of one tool call and returns the _meta to send with it. Call
// release when the call is done. When ctx carries neither it returns nil and a
// no-op release.
func ActingUserMeta(ctx context.Context) (meta map[string]any, release func()) {
	u := UserFromContext(ctx)
	g := voiceGrantFrom(ctx)
	fs := filesFromContext(ctx)
	if u == nil && g == (voiceGrant{}) && fs == nil {
		return nil, func() {}
	}
	token := uuid.NewString()
	actingUsers.Store(token, actingEntry{user: u, grant: g, files: fs})
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
		e := v.(actingEntry)
		ctx = WithUser(ctx, e.user)
		ctx = WithFiles(ctx, e.files)
		return context.WithValue(ctx, voiceGrantKey{}, e.grant)
	}
	return ctx
}
