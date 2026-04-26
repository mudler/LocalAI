# API Endpoints and Authentication

This guide covers how to add new API endpoints and properly integrate them with the auth/permissions system.

> **Before you ship a new endpoint or capability surface**, re-read the [checklist at the bottom of this file](#checklist). LocalAI advertises its feature surface in several independent places — miss any one of them and clients/admins/UI won't know the endpoint exists.

## Architecture overview

Authentication and authorization flow through three layers:

1. **Global auth middleware** (`core/http/auth/middleware.go` → `auth.Middleware`) — applied to every request in `core/http/app.go`. Handles session cookies, Bearer tokens, API keys, and legacy API keys. Populates `auth_user` and `auth_role` in the Echo context.
2. **Feature middleware** (`auth.RequireFeature`) — per-feature access control applied to route groups or individual routes. Checks if the authenticated user has the specific feature enabled.
3. **Admin middleware** (`auth.RequireAdmin`) — restricts endpoints to admin users only.

When auth is disabled (no auth DB, no legacy API keys), all middleware becomes pass-through (`auth.NoopMiddleware`).

## Adding a new API endpoint

### Step 1: Create the handler

Write the endpoint handler in the appropriate package under `core/http/endpoints/`. Follow existing patterns:

```go
// core/http/endpoints/localai/my_feature.go
func MyFeatureEndpoint(app *application.Application) echo.HandlerFunc {
    return func(c echo.Context) error {
        // Use auth.GetUser(c) to get the authenticated user (may be nil if auth is disabled)
        user := auth.GetUser(c)

        // Your logic here
        return c.JSON(http.StatusOK, result)
    }
}
```

### Step 2: Register routes

Add routes in the appropriate file under `core/http/routes/`. The file you use depends on the endpoint category:

| File | Category |
|------|----------|
| `routes/openai.go` | OpenAI-compatible API endpoints (`/v1/...`) |
| `routes/localai.go` | LocalAI-specific endpoints (`/api/...`, `/models/...`, `/backends/...`) |
| `routes/agents.go` | Agent pool endpoints (`/api/agents/...`) |
| `routes/auth.go` | Auth endpoints (`/api/auth/...`) |
| `routes/ui_api.go` | UI backend API endpoints |

### Step 3: Apply the right middleware

Choose the appropriate protection level:

#### No auth required (public)
Exempt paths bypass auth entirely. Add to `isExemptPath()` in `middleware.go` or use the `/api/auth/` prefix (always exempt). Use sparingly — most endpoints should require auth.

#### Standard auth (any authenticated user)
The global middleware already handles this. API paths (`/api/`, `/v1/`, etc.) automatically require authentication when auth is enabled. You don't need to add any extra middleware.

```go
router.GET("/v1/my-endpoint", myHandler)  // auth enforced by global middleware
```

#### Admin only
Pass `adminMiddleware` to the route. This is set up in `app.go` and passed to `Register*Routes` functions:

```go
// In the Register function signature, accept the middleware:
func RegisterMyRoutes(router *echo.Echo, app *application.Application, adminMiddleware echo.MiddlewareFunc) {
    router.POST("/models/apply", myHandler, adminMiddleware)
}
```

#### Feature-gated
For endpoints that should be toggleable per-user, use feature middleware. There are two approaches:

**Approach A: Route-level middleware** (preferred for groups of related endpoints)

```go
// In app.go, create the feature middleware:
myFeatureMw := auth.RequireFeature(application.AuthDB(), auth.FeatureMyFeature)

// Pass it to the route registration function:
routes.RegisterMyRoutes(e, app, myFeatureMw)

// In the routes file, apply to a group:
g := e.Group("/api/my-feature", myFeatureMw)
g.GET("", listHandler)
g.POST("", createHandler)
```

**Approach B: RouteFeatureRegistry** (preferred for individual OpenAI-compatible endpoints)

Add an entry to `RouteFeatureRegistry` in `core/http/auth/features.go`. The `RequireRouteFeature` global middleware will automatically enforce it:

```go
var RouteFeatureRegistry = []RouteFeature{
    // ... existing entries ...
    {"POST", "/v1/my-endpoint", FeatureMyFeature},
}
```

## Adding a new feature

When you need a new toggleable feature (not just a new endpoint under an existing feature):

### 1. Define the feature constant

Add to `core/http/auth/permissions.go`:

```go
const (
    // Add to the appropriate group:
    // Agent features (default OFF for new users)
    FeatureMyFeature = "my_feature"

    // OR API features (default ON for new users)
    FeatureMyFeature = "my_feature"
)
```

Then add it to the appropriate slice:

```go
// Default OFF — user must be explicitly granted access:
var AgentFeatures = []string{..., FeatureMyFeature}

// Default ON — user has access unless explicitly revoked:
var APIFeatures = []string{..., FeatureMyFeature}
```

### 2. Add feature metadata

In `core/http/auth/features.go`, add to the appropriate `FeatureMetas` function so the admin UI can display it:

```go
func AgentFeatureMetas() []FeatureMeta {
    return []FeatureMeta{
        // ... existing ...
        {FeatureMyFeature, "My Feature", false},  // false = default OFF
    }
}
```

### 3. Wire up the middleware

In `core/http/app.go`:

```go
myFeatureMw := auth.RequireFeature(application.AuthDB(), auth.FeatureMyFeature)
```

Then pass it to the route registration function.

### 4. Register route-feature mappings (if applicable)

If your feature gates standard API endpoints (like `/v1/...`), add entries to `RouteFeatureRegistry` in `features.go` instead of using per-route middleware.

## Accessing the authenticated user in handlers

```go
import "github.com/mudler/LocalAI/core/http/auth"

func MyHandler(c echo.Context) error {
    // Get the user (nil when auth is disabled or unauthenticated)
    user := auth.GetUser(c)
    if user == nil {
        // Handle unauthenticated — or let middleware handle it
    }

    // Check role
    if user.Role == auth.RoleAdmin {
        // admin-specific logic
    }

    // Check feature access programmatically (when you need conditional behavior, not full blocking)
    if auth.HasFeatureAccess(db, user, auth.FeatureMyFeature) {
        // feature-specific logic
    }

    // Check model access
    if !auth.IsModelAllowed(db, user, modelName) {
        return c.JSON(http.StatusForbidden, ...)
    }
}
```

## Middleware composition patterns

Middleware can be composed at different levels. Here are the patterns used in the codebase:

### Group-level middleware (agents pattern)
```go
// All routes in the group share the middleware
g := e.Group("/api/agents", poolReadyMw, agentsMw)
g.GET("", listHandler)
g.POST("", createHandler)
```

### Per-route middleware (localai pattern)
```go
// Individual routes get middleware as extra arguments
router.POST("/models/apply", applyHandler, adminMiddleware)
router.GET("/metrics", metricsHandler, adminMiddleware)
```

### Middleware slice (openai pattern)
```go
// Build a middleware chain for a handler
chatMiddleware := []echo.MiddlewareFunc{
    usageMiddleware,
    traceMiddleware,
    modelFilterMiddleware,
}
app.POST("/v1/chat/completions", chatHandler, chatMiddleware...)
```

## Error response format

Always use `schema.ErrorResponse` for auth/permission errors to stay consistent with the OpenAI-compatible API:

```go
return c.JSON(http.StatusForbidden, schema.ErrorResponse{
    Error: &schema.APIError{
        Message: "feature not enabled for your account",
        Code:    http.StatusForbidden,
        Type:    "authorization_error",
    },
})
```

Use these HTTP status codes:
- `401 Unauthorized` — no valid credentials provided
- `403 Forbidden` — authenticated but lacking permission
- `429 Too Many Requests` — rate limited (auth endpoints)

## Usage tracking

If your endpoint should be tracked for usage (token counts, request counts), add the `usageMiddleware` to its middleware chain. See `core/http/middleware/usage.go` and how it's applied in `routes/openai.go`.

## Advertising surfaces — where to register a new capability

Beyond routing and auth, LocalAI publishes its capability surface in **four independent places**. When you add an endpoint — especially one introducing a net-new capability like a new media type or a new auth-gated feature — you must update every relevant surface. These aren't optional: missing them means the endpoint works but is invisible to clients, admins, and the UI.

### 1. Swagger `@Tags` annotation (mandatory)

Every handler needs a swagger block so the endpoint appears in `/swagger/index.html` and in the `/api/instructions` output. The `@Tags` value is what groups the endpoint into a capability area:

```go
// MyEndpoint does X.
// @Summary Do X.
// @Tags my-capability
// @Param request body schema.MyRequest true "payload"
// @Success 200 {object} schema.MyResponse "Response"
// @Router /v1/my-endpoint [post]
func MyEndpoint(...) echo.HandlerFunc { ... }
```

Use an existing tag when the endpoint extends an existing area (e.g. `audio`, `images`, `face-recognition`). Create a new tag only when the endpoint introduces a genuinely new capability surface — and in that case, also register it in step 2.

After adding endpoints, regenerate the embedded spec so the runtime serves it:

```bash
make protogen-go         # ensures gRPC codegen is fresh first
make swagger             # regenerates swagger/swagger.json
```

### 2. `/api/instructions` registry (for new capability areas)

`core/http/endpoints/localai/api_instructions.go` defines `instructionDefs` — a lightweight, machine-readable index of capability areas that groups swagger endpoints by tag. It's the primary discovery surface for agents and SDKs ("what can this server do?").

**When to update:** only when adding a new capability area (a new swagger tag). Existing-tag additions automatically surface without any change here.

Add an entry to `instructionDefs`:

```go
{
    Name:        "my-capability",             // URL segment at /api/instructions/my-capability
    Description: "Short sentence describing the capability",
    Tags:        []string{"my-capability"},   // must match swagger @Tags
    Intro:       "Optional gotcha/context that isn't in the swagger descriptions (caveats, defaults, cross-references to other endpoints).",
},
```

Also bump the expected-length count in `api_instructions_test.go` and add the name to the `ContainElements` assertion.

### 3. `capabilities.js` symbol (for new model-config FLAG_* flags)

If your feature needs a new `FLAG_*` usecase flag in `core/config/model_config.go` (so users can filter gallery models by it, and so `/v1/models` surfaces it), also declare the matching symbol in `core/http/react-ui/src/utils/capabilities.js`:

```js
export const CAP_MY_CAPABILITY = 'FLAG_MY_CAPABILITY'
```

React pages that want to filter the ModelSelector by capability import this symbol. Declare it even if you're not building the UI page yet — the declaration keeps the Go/JS vocabularies in sync.

### 4. `docs/content/` (user-facing documentation)

A new capability deserves its own page under `docs/content/features/`, plus cross-links from related features and an entry in `docs/content/whats-new.md`. See the pattern used by `face-recognition.md` / `object-detection.md`.

## Path protection rules

The global auth middleware classifies paths as API paths or non-API paths:

- **API paths** (always require auth when auth is enabled): `/api/`, `/v1/`, `/models/`, `/backends/`, `/backend/`, `/tts`, `/vad`, `/video`, `/stores/`, `/system`, `/ws/`, `/metrics`
- **Exempt paths** (never require auth): `/api/auth/` prefix, anything in `appConfig.PathWithoutAuth`
- **Non-API paths** (UI, static assets): pass through without auth — the React UI handles login redirects client-side

If you add endpoints under a new top-level path prefix, add it to `isAPIPath()` in `middleware.go` to ensure it requires authentication.

## Checklist

When adding a new endpoint:

**Routing & auth**
- [ ] Handler in `core/http/endpoints/`
- [ ] Route registered in appropriate `core/http/routes/` file
- [ ] Auth level chosen: public / standard / admin / feature-gated
- [ ] Entry added to `RouteFeatureRegistry` in `core/http/auth/features.go` (one row per route/method — all /v1/* routes gate through this, not per-route middleware)
- [ ] If new feature: constant in `permissions.go`, added to the right slice (`APIFeatures` default-ON / `AgentFeatures` default-OFF), metadata in `features.go` `*FeatureMetas()`
- [ ] If feature uses group middleware: wired in `core/http/app.go` and passed to the route registration function
- [ ] If new path prefix: added to `isAPIPath()` in `middleware.go`
- [ ] If token-counting: `usageMiddleware` added to middleware chain

**Advertising surfaces (easy to miss — see the [Advertising surfaces](#advertising-surfaces--where-to-register-a-new-capability) section)**
- [ ] Swagger block on the handler: `@Summary`, `@Tags`, `@Param`, `@Success`, `@Router`
- [ ] If new capability area (new swagger tag): entry in `instructionDefs` in `core/http/endpoints/localai/api_instructions.go` + test count bumped in `api_instructions_test.go`
- [ ] If new `FLAG_*` usecase flag: matching `CAP_*` symbol exported from `core/http/react-ui/src/utils/capabilities.js`
- [ ] `docs/content/features/<feature>.md` created; cross-links from related feature pages; entry in `docs/content/whats-new.md`

**Quality**
- [ ] Error responses use `schema.ErrorResponse` format (or `echo.NewHTTPError` with a mapped gRPC status — see the `mapBackendError` helper in `core/http/endpoints/localai/images.go`)
- [ ] Tests cover both authenticated and unauthenticated access
- [ ] Swagger regenerated (`make swagger`) if you changed any `@Router`/`@Tags`/`@Param` annotation
