// Package plugins serves the REST API of the plugins that are compatible with
// the Claude Code plugin format (/api/plugins). The handlers keep no state:
// each request reads the Plugins service, which reads the shared store.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// ConfirmHeader must name what the administrator approved in the browser:
// the plugin ID for an install of a plugin that runs code, the install ID for
// an update or a rollback. A form post or a link cannot set it.
const ConfirmHeader = "X-LocalAI-Confirm"

// Error types of the answers, in schema.APIError.Type.
const (
	ErrTypeUnavailable      = "plugins_unavailable"
	ErrTypeNotHumanSurface  = "not_human_surface"
	ErrTypeNeedsAdmin       = "needs_admin"
	ErrTypeCodeForEveryone  = "code_for_everyone"
	ErrTypeBlocked          = "blocked"
	ErrTypeForbidden        = "forbidden"
	ErrTypeNotFound         = "not_found"
	ErrTypeInvalidToken     = "invalid_token"
	ErrTypeInvalid          = "invalid_request"
	ErrTypeUnsupported      = "unsupported"
	ErrTypeLimit            = "limit_reached"
	ErrTypeRefused          = "content_refused"
	ErrTypeConflict         = "conflict"
	ErrTypeInstanceInstall  = "instance_installed"
	ErrTypeNoUpdate         = "no_update"
	ErrTypeNeedsReenable    = "needs_reenable"
	ErrTypeDigestMismatch   = "digest_mismatch"
	ErrTypeUpstream         = "fetch_failed"
	ErrTypeChatsUnsupported = "chat_switch_unavailable"
	ErrTypeRateLimited      = "rate_limited"
)

// ChatSwitch keeps the plugins a chat has switched off (spec 6.6). The chats
// service implements it; without one the route answers 501.
type ChatSwitch interface {
	SetPluginsOff(ctx context.Context, user, chatID string, off []string) error
}

// Deps is what the handlers need.
type Deps struct {
	// Service returns the Plugins service, or nil when it is off; Unavailable
	// then says why.
	Service     func() *plugins.Service
	Unavailable func() string
	// AuthOn is set when the server checks credentials (accounts or shared
	// API keys). With no authentication the one caller is the local owner,
	// an administrator.
	AuthOn bool
	// OpCache lists the install and update jobs in /api/operations. Nil
	// leaves them out of the list.
	OpCache *galleryop.OpCache
	// Chats sets the plugin switches of a chat. Nil (no chats) answers 501.
	Chats ChatSwitch
}

// Handlers serves the routes.
type Handlers struct{ d Deps }

// New returns the handlers.
func New(d Deps) *Handlers { return &Handlers{d: d} }

func apiError(c echo.Context, code int, typ, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Code: code, Type: typ, Message: msg}})
}

// service returns the service, or answers 503 and returns nil.
func (h *Handlers) service(c echo.Context) *plugins.Service {
	var svc *plugins.Service
	if h.d.Service != nil {
		svc = h.d.Service()
	}
	if svc == nil {
		msg := "Plugins are not set up on this server."
		if h.d.Unavailable != nil && h.d.Unavailable() != "" {
			msg = h.d.Unavailable()
		}
		_ = apiError(c, http.StatusServiceUnavailable, ErrTypeUnavailable, msg)
	}
	return svc
}

// actor builds the caller from the authentication middleware. With no
// authentication on the server the caller is the local owner, the same
// principal the chats use.
func (h *Handlers) actor(c echo.Context) (plugins.Actor, bool) {
	u := auth.GetUser(c)
	if u == nil || u.ID == "" {
		if h.d.AuthOn {
			_ = apiError(c, http.StatusUnauthorized, "authentication_error", "Authentication required")
			return plugins.Actor{}, false
		}
		return plugins.Actor{ID: notify.AnonymousPrincipal, Admin: true}, true
	}
	return plugins.Actor{ID: u.ID, Admin: u.Role == auth.RoleAdmin}, true
}

// prepare returns the service and the caller, or answers and returns false.
func (h *Handlers) prepare(c echo.Context) (*plugins.Service, plugins.Actor, bool) {
	svc := h.service(c)
	if svc == nil {
		return nil, plugins.Actor{}, false
	}
	a, ok := h.actor(c)
	return svc, a, ok
}

// humanSurface reports whether the request comes from a person's browser tab
// that confirms what it names (spec 9.2), as the add-on host grants require:
//
//  1. The credential is the web UI's own (auth.IsWebSession): a session
//     cookie, the UI's token cookie, or no authentication at all. A key in a
//     header, a user API key and an in-process call are not.
//  2. It is a same-origin fetch: Sec-Fetch-Site same-origin with destination
//     empty, or, where the browser sends no fetch metadata, an Origin or
//     Referer of this host.
//  3. It carries no tool, agent or MCP marker.
//  4. ConfirmHeader equals confirm, which a form post or a link cannot set.
func (h *Handlers) humanSurface(c echo.Context, confirm string) bool {
	r := c.Request()
	hd := r.Header
	if confirm == "" || hd.Get(ConfirmHeader) != confirm {
		return false
	}
	for _, name := range []string{"Authorization", "X-Api-Key", "Xi-Api-Key"} {
		if hd.Get(name) != "" {
			return false
		}
	}
	if auth.GetAPIKey(c) != nil || !auth.IsWebSession(c, h.d.AuthOn) {
		return false
	}
	if auth.HasToolContext(hd) {
		return false
	}
	return auth.SameOriginFetch(r)
}

// fail maps an error of the service to its answer (spec 16).
func fail(c echo.Context, err error) error {
	var inst *store.InstanceInstalledError
	switch {
	case errors.As(err, &inst):
		id := inst.InstallID
		return c.JSON(http.StatusConflict, schema.ErrorResponse{Error: &schema.APIError{
			Code: http.StatusConflict, Type: ErrTypeInstanceInstall, Param: &id,
			Message: "The plugin is installed for everyone already (install " + id + ").",
		}})
	case errors.Is(err, plugins.ErrTokenInvalid):
		return apiError(c, http.StatusBadRequest, ErrTypeInvalidToken, err.Error())
	case errors.Is(err, plugins.ErrNotHumanSurface):
		return apiError(c, http.StatusForbidden, ErrTypeNotHumanSurface, "This needs the administrator's own browser session.")
	case errors.Is(err, plugins.ErrNeedsAdmin):
		return apiError(c, http.StatusForbidden, ErrTypeNeedsAdmin, err.Error())
	case errors.Is(err, plugins.ErrCodeForEveryone):
		return apiError(c, http.StatusForbidden, ErrTypeCodeForEveryone, err.Error())
	case errors.Is(err, plugins.ErrBlocked):
		return apiError(c, http.StatusForbidden, ErrTypeBlocked, err.Error())
	case errors.Is(err, plugins.ErrForbidden):
		return apiError(c, http.StatusForbidden, ErrTypeForbidden, err.Error())
	case errors.Is(err, plugins.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return apiError(c, http.StatusNotFound, ErrTypeNotFound, err.Error())
	case errors.Is(err, plugins.ErrNoUpdate):
		return apiError(c, http.StatusConflict, ErrTypeNoUpdate, err.Error())
	case errors.Is(err, plugins.ErrNeedsReenable):
		return apiError(c, http.StatusConflict, ErrTypeNeedsReenable, err.Error())
	case errors.Is(err, plugins.ErrDigestMismatch), errors.Is(err, content.ErrDigestMismatch):
		return apiError(c, http.StatusConflict, ErrTypeDigestMismatch, err.Error())
	case errors.Is(err, store.ErrConflict):
		return apiError(c, http.StatusConflict, ErrTypeConflict, err.Error())
	case errors.Is(err, plugins.ErrUnsupported):
		return apiError(c, http.StatusUnprocessableEntity, ErrTypeUnsupported, err.Error())
	case errors.Is(err, plugins.ErrRateLimited):
		return apiError(c, http.StatusTooManyRequests, ErrTypeRateLimited, err.Error())
	case errors.Is(err, plugins.ErrLimit):
		return apiError(c, http.StatusUnprocessableEntity, ErrTypeLimit, err.Error())
	case errors.Is(err, content.ErrRefused):
		return apiError(c, http.StatusUnprocessableEntity, ErrTypeRefused, err.Error())
	case errors.Is(err, plugins.ErrInvalid), errors.Is(err, store.ErrInvalid), errors.Is(err, fetch.ErrRefusedURL):
		return apiError(c, http.StatusBadRequest, ErrTypeInvalid, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return apiError(c, http.StatusServiceUnavailable, ErrTypeUpstream, "The request was cancelled or timed out.")
	}
	// A fetch, git or store failure: its text can name folders of the
	// server, so the log keeps it and the answer does not.
	xlog.Warn("plugins: request failed", "path", c.Path(), "error", err)
	return apiError(c, http.StatusBadGateway, ErrTypeUpstream, "The plugin or the marketplace could not be read. See the server log.")
}

// maxBody bounds a request body.
const maxBody = 1 << 20

// decode reads a JSON body strictly: no unknown field, no trailing data.
func decode(c echo.Context, v any) error {
	r := http.MaxBytesReader(c.Response(), c.Request().Body, maxBody)
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing data after the JSON body")
	}
	return nil
}

func badBody(c echo.Context, err error) error {
	return apiError(c, http.StatusBadRequest, ErrTypeInvalid, "Invalid request body: "+err.Error())
}

// filePath returns the file path of a wildcard route.
func filePath(c echo.Context) string {
	p := c.Param("*")
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	return p
}

// serveFile writes one file of a package as an attachment-free plain view.
// The bytes are plugin text for review, never a page: the type is text and
// the browser must not sniff it.
func serveFile(c echo.Context, r io.ReadSeekCloser) error {
	defer r.Close()
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	c.Response().Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	return c.Stream(http.StatusOK, "text/plain; charset=utf-8", r)
}

// trackJob shows a queued job in /api/operations.
func (h *Handlers) trackJob(pluginID, jobID string) {
	if h.d.OpCache != nil && jobID != "" {
		h.d.OpCache.Set(galleryop.PluginOpKey(pluginID), jobID)
	}
}
