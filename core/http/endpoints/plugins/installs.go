package plugins

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// Inspect fetches a plugin for review.
// @Summary Inspect a plugin before an install
// @Description Fetches the plugin at the commit its marketplace entry points at, checks every file, computes the content digest, reads what it contributes and whether it runs code, and returns a preview token. The token works once, for 15 minutes, for the caller only. Any user with the plugins feature may inspect.
// @Tags Plugins
// @Accept json
// @Produce json
// @Param request body InspectRequest true "Plugin ID (name@marketplace)"
// @Success 200 {object} plugins.Preview "The preview and its token"
// @Failure 400 {object} schema.ErrorResponse "Not name@marketplace, or an invalid plugin"
// @Failure 404 {object} schema.ErrorResponse "No such entry, or its marketplace is off"
// @Failure 422 {object} schema.ErrorResponse "A source type LocalAI does not fetch (unsupported), or files the checks refuse (content_refused)"
// @Failure 429 {object} schema.ErrorResponse "Too many inspects of this user, or too many at once on this server (rate_limited)"
// @Failure 502 {object} schema.ErrorResponse "The plugin could not be fetched"
// @Router /api/plugins/inspect [post]
func (h *Handlers) Inspect(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	var req InspectRequest
	if err := decode(c, &req); err != nil {
		return badBody(c, err)
	}
	p, err := svc.Inspect(c.Request().Context(), a, req.Plugin)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// PreviewFile returns one file of a preview.
// @Summary One file of a preview, for review
// @Description Returns one file of the package that a preview token (of an inspect or of an update preview) names, as plain text. It does not use the token up. Only the caller who made the preview can read it.
// @Tags Plugins
// @Produce plain
// @Param token path string true "Preview token"
// @Param path path string true "File path inside the plugin"
// @Success 200 {string} string "The file"
// @Failure 400 {object} schema.ErrorResponse "Invalid or expired token, or an invalid path"
// @Failure 404 {object} schema.ErrorResponse "No such file"
// @Router /api/plugins/inspect/{token}/files/{path} [get]
func (h *Handlers) PreviewFile(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	r, err := svc.PreviewFile(c.Request().Context(), a, c.Param("token"), filePath(c))
	if err != nil {
		return fail(c, err)
	}
	return serveFile(c, r)
}

// Install confirms a preview and queues the install.
// @Summary Install a plugin
// @Description Confirms a preview token and queues the install; the job fetches the plugin again at the same commit and refuses files that differ from the reviewed ones. scope is user (default: for the caller only, a plugin that runs no code, while plugins_user_installs is on) or instance (for everyone, administrators only). A plugin that runs code is installed for everyone only, by an administrator from the browser: the request needs the administrator's own browser session and the header X-LocalAI-Confirm set to the plugin ID; an API key or an MCP call gets 403 not_human_surface. The progress is in GET /api/operations (isPlugin).
// @Tags Plugins
// @Accept json
// @Produce json
// @Param request body InstallRequest true "Token and scope"
// @Param X-LocalAI-Confirm header string false "The plugin ID, for a plugin that runs code"
// @Success 202 {object} JobAccepted "Queued"
// @Failure 400 {object} schema.ErrorResponse "Invalid or used token (invalid_token), or an invalid scope"
// @Failure 403 {object} schema.ErrorResponse "needs_admin, code_for_everyone, forbidden or not_human_surface"
// @Failure 409 {object} schema.ErrorResponse "Installed already (conflict), or installed for everyone (instance_installed, param names the install)"
// @Failure 422 {object} schema.ErrorResponse "Too many installs (limit_reached)"
// @Router /api/plugins/installs [post]
func (h *Handlers) Install(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	var req InstallRequest
	if err := decode(c, &req); err != nil {
		return badBody(c, err)
	}
	if req.Scope == "" {
		req.Scope = store.ScopeUser
	}
	ctx := c.Request().Context()
	t, err := svc.PreviewToken(ctx, a, req.Token)
	if err != nil {
		return fail(c, err)
	}
	a.HumanSurface = h.humanSurface(c, t.PluginID)
	job, err := svc.Install(ctx, a, req.Token, req.Scope)
	if err != nil {
		return fail(c, err)
	}
	h.trackJob(t.PluginID, job)
	return c.JSON(http.StatusAccepted, JobAccepted{JobID: job, PluginID: t.PluginID})
}

// ListInstalls lists the installs.
// @Summary List the plugin installs
// @Description Lists the installs for everyone and the caller's own, with the caller's switch. An administrator adds all=true to list the personal installs of every user. Needs the plugins feature.
// @Tags Plugins
// @Produce json
// @Param all query bool false "Every install (administrators only)"
// @Success 200 {object} InstallList "Installs"
// @Failure 403 {object} schema.ErrorResponse "all=true for a user"
// @Router /api/plugins/installs [get]
func (h *Handlers) ListInstalls(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	list, err := svc.ListInstalls(c.Request().Context(), a, c.QueryParam("all") == "true")
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, InstallList{Installs: list})
}

// GetInstall returns one install.
// @Summary One plugin install
// @Description Returns an install with what its package contributes, the components left out with their reasons, the code files and the hosts its remote MCP servers connect to. A personal install of another user is not found, except for an administrator.
// @Tags Plugins
// @Produce json
// @Param id path string true "Install ID"
// @Success 200 {object} plugins.InstallDetail "The install"
// @Failure 404 {object} schema.ErrorResponse "No such install"
// @Router /api/plugins/installs/{id} [get]
func (h *Handlers) GetInstall(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	d, err := svc.GetInstall(c.Request().Context(), a, c.Param("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

// InstallFile returns one file of an install.
// @Summary One file of an installed plugin
// @Description Returns one file of the installed package as plain text.
// @Tags Plugins
// @Produce plain
// @Param id path string true "Install ID"
// @Param path path string true "File path inside the plugin"
// @Success 200 {string} string "The file"
// @Failure 400 {object} schema.ErrorResponse "Invalid path"
// @Failure 404 {object} schema.ErrorResponse "No such install or file"
// @Router /api/plugins/installs/{id}/files/{path} [get]
func (h *Handlers) InstallFile(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	r, err := svc.InstallFile(c.Request().Context(), a, c.Param("id"), filePath(c))
	if err != nil {
		return fail(c, err)
	}
	return serveFile(c, r)
}

// PreviewUpdate fetches the new version of an install for review.
// @Summary Preview the update of a plugin
// @Description Fetches the newer version and returns the files added, removed and changed, the unified diffs of the changed code files (256 KiB in all), the old and new code reasons and a preview token. The owner of a personal install or an administrator may ask. A personal install whose new version runs code is blocked and keeps its version (403 blocked).
// @Tags Plugins
// @Produce json
// @Param id path string true "Install ID"
// @Success 200 {object} plugins.UpdatePreview "The diff and its token"
// @Failure 403 {object} schema.ErrorResponse "blocked or forbidden"
// @Failure 404 {object} schema.ErrorResponse "No such install"
// @Failure 409 {object} schema.ErrorResponse "No update available (no_update)"
// @Router /api/plugins/installs/{id}/update/preview [post]
func (h *Handlers) PreviewUpdate(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	p, err := svc.PreviewUpdate(c.Request().Context(), a, c.Param("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// Update confirms an update preview and queues the update.
// @Summary Update a plugin
// @Description Confirms an update preview and queues the update. When the preview has needsCodeReview, the request needs the administrator's own browser session with the header X-LocalAI-Confirm set to the install ID (else 403 not_human_surface), and approved_files must name every changed code file. The progress is in GET /api/operations (isPlugin).
// @Tags Plugins
// @Accept json
// @Produce json
// @Param id path string true "Install ID"
// @Param request body UpdateRequest true "Token and approved code files"
// @Param X-LocalAI-Confirm header string false "The install ID, for an update whose code changed"
// @Success 202 {object} JobAccepted "Queued"
// @Failure 400 {object} schema.ErrorResponse "Invalid token, or a changed code file not approved"
// @Failure 403 {object} schema.ErrorResponse "blocked, forbidden or not_human_surface"
// @Failure 404 {object} schema.ErrorResponse "No such install"
// @Router /api/plugins/installs/{id}/update [post]
func (h *Handlers) Update(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	var req UpdateRequest
	if err := decode(c, &req); err != nil {
		return badBody(c, err)
	}
	ctx := c.Request().Context()
	id := c.Param("id")
	in, err := svc.GetInstall(ctx, a, id)
	if err != nil {
		return fail(c, err)
	}
	a.HumanSurface = h.humanSurface(c, id)
	job, err := svc.Update(ctx, a, id, req.Token, req.ApprovedFiles)
	if err != nil {
		return fail(c, err)
	}
	h.trackJob(in.PluginID, job)
	return c.JSON(http.StatusAccepted, JobAccepted{JobID: job, PluginID: in.PluginID})
}

// Rollback sets the previous version of an install again.
// @Summary Roll a plugin back
// @Description Sets the previous version of an install again, with the rights of an update; the version rolled back from becomes the available update. For an install for everyone that runs code, in either version, the request needs the administrator's own browser session with X-LocalAI-Confirm set to the install ID.
// @Tags Plugins
// @Produce json
// @Param id path string true "Install ID"
// @Param X-LocalAI-Confirm header string false "The install ID, when code is involved"
// @Success 200 {object} plugins.InstallDetail "The install after the rollback"
// @Failure 400 {object} schema.ErrorResponse "No previous version"
// @Failure 403 {object} schema.ErrorResponse "blocked, forbidden or not_human_surface"
// @Failure 404 {object} schema.ErrorResponse "No such install, or the previous version is gone"
// @Failure 409 {object} schema.ErrorResponse "The install changed meanwhile"
// @Router /api/plugins/installs/{id}/rollback [post]
func (h *Handlers) Rollback(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	id := c.Param("id")
	a.HumanSurface = h.humanSurface(c, id)
	if err := svc.Rollback(ctx, a, id); err != nil {
		return fail(c, err)
	}
	d, err := svc.GetInstall(ctx, a, id)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

// Remove deletes an install.
// @Summary Remove a plugin
// @Description Removes an install and the switches of its users. The owner removes a personal install; an administrator removes any install.
// @Tags Plugins
// @Param id path string true "Install ID"
// @Success 204 "Removed"
// @Failure 403 {object} schema.ErrorResponse "forbidden"
// @Failure 404 {object} schema.ErrorResponse "No such install"
// @Router /api/plugins/installs/{id} [delete]
func (h *Handlers) Remove(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	if err := svc.Remove(c.Request().Context(), a, c.Param("id")); err != nil {
		return fail(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// GetMyState returns the caller's state of an install.
// @Summary My state of a plugin
// @Description Returns whether the caller has the install on, whether it waits for a new confirmation because it now runs code, the caller's settings, and which secret settings are set.
// @Tags Plugins
// @Produce json
// @Param id path string true "Install ID"
// @Success 200 {object} plugins.MyState "The state"
// @Failure 403 {object} schema.ErrorResponse "A personal install of another user"
// @Failure 404 {object} schema.ErrorResponse "No such install"
// @Router /api/plugins/installs/{id}/me [get]
func (h *Handlers) GetMyState(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	s, err := svc.MyState(c.Request().Context(), a, c.Param("id"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, s)
}

// SetMyState changes the caller's state of an install.
// @Summary Turn a plugin on or off for me
// @Description Turns an install on or off for the caller and sets their settings. Turning off keeps the settings. An install that runs code since the caller turned it on answers 409 needs_reenable until the request also sends reenable true. Secret settings are not supported yet (422).
// @Tags Plugins
// @Accept json
// @Produce json
// @Param id path string true "Install ID"
// @Param request body MyStateRequest true "Changes"
// @Success 200 {object} plugins.MyState "The state"
// @Failure 400 {object} schema.ErrorResponse "A setting the plugin does not have"
// @Failure 403 {object} schema.ErrorResponse "A personal install of another user"
// @Failure 404 {object} schema.ErrorResponse "No such install"
// @Failure 409 {object} schema.ErrorResponse "needs_reenable"
// @Failure 422 {object} schema.ErrorResponse "Secret settings (unsupported)"
// @Router /api/plugins/installs/{id}/me [put]
func (h *Handlers) SetMyState(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	var req MyStateRequest
	if err := decode(c, &req); err != nil {
		return badBody(c, err)
	}
	s, err := svc.SetMyState(c.Request().Context(), a, c.Param("id"), plugins.SetMyStateRequest{
		Enabled: req.Enabled, Reenable: req.Reenable, Config: req.Config, Secrets: req.Secrets,
	})
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, s)
}
