package plugins

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/services/plugins"
)

// ListMarketplaces lists the marketplaces.
// @Summary List the plugin marketplaces
// @Description Lists the marketplaces of plugins compatible with the Claude Code plugin format, with their state and the short code of the last failed refresh. Needs the plugins feature.
// @Tags Plugins
// @Produce json
// @Success 200 {object} MarketplaceList "Marketplaces"
// @Failure 401 {object} schema.ErrorResponse "Authentication required"
// @Failure 403 {object} schema.ErrorResponse "The plugins feature is off for the caller"
// @Failure 503 {object} schema.ErrorResponse "Plugins are not set up on this server (plugins_unavailable)"
// @Router /api/plugins/marketplaces [get]
func (h *Handlers) ListMarketplaces(c echo.Context) error {
	svc, _, ok := h.prepare(c)
	if !ok {
		return nil
	}
	list, err := svc.ListMarketplaces(c.Request().Context())
	if err != nil {
		return fail(c, err)
	}
	out := MarketplaceList{Marketplaces: make([]Marketplace, 0, len(list))}
	for _, m := range list {
		out.Marketplaces = append(out.Marketplaces, marketplaceOf(m))
	}
	return c.JSON(http.StatusOK, out)
}

// AddMarketplace adds a marketplace and reads it at once.
// @Summary Add a plugin marketplace
// @Description Adds a marketplace (owner/repo, an https git URL or an https URL of a marketplace.json file) and reads it at once. Nothing is kept when the first read fails. Administrators only.
// @Tags Plugins
// @Accept json
// @Produce json
// @Param request body AddMarketplaceRequest true "Source"
// @Success 201 {object} Marketplace "The new marketplace"
// @Failure 400 {object} schema.ErrorResponse "Invalid source, ref or ID, or the marketplace could not be read"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 409 {object} schema.ErrorResponse "The ID is taken"
// @Failure 422 {object} schema.ErrorResponse "Too many marketplaces (limit_reached)"
// @Router /api/plugins/marketplaces [post]
func (h *Handlers) AddMarketplace(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	var req AddMarketplaceRequest
	if err := decode(c, &req); err != nil {
		return badBody(c, err)
	}
	m, err := svc.AddMarketplace(c.Request().Context(), a, plugins.AddMarketplaceRequest{Source: req.Source, Ref: req.Ref, ID: req.ID})
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusCreated, marketplaceOf(m))
}

// UpdateMarketplace changes the ref or the switch of a marketplace.
// @Summary Change a plugin marketplace
// @Description Changes the ref (read at the next refresh) or the enabled switch of a marketplace. Administrators only.
// @Tags Plugins
// @Accept json
// @Produce json
// @Param id path string true "Marketplace ID"
// @Param request body UpdateMarketplaceRequest true "Changes"
// @Success 200 {object} Marketplace "The marketplace"
// @Failure 400 {object} schema.ErrorResponse "Invalid ref"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 404 {object} schema.ErrorResponse "No such marketplace"
// @Router /api/plugins/marketplaces/{id} [put]
func (h *Handlers) UpdateMarketplace(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	var req UpdateMarketplaceRequest
	if err := decode(c, &req); err != nil {
		return badBody(c, err)
	}
	m, err := svc.UpdateMarketplace(c.Request().Context(), a, c.Param("id"), plugins.UpdateMarketplaceRequest{Ref: req.Ref, Enabled: req.Enabled})
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, marketplaceOf(m))
}

// RemoveMarketplace removes a marketplace.
// @Summary Remove a plugin marketplace
// @Description Removes a marketplace and its list. A default marketplace stays removed and is not added again at start. The installs from it stay. Administrators only.
// @Tags Plugins
// @Param id path string true "Marketplace ID"
// @Success 204 "Removed"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 404 {object} schema.ErrorResponse "No such marketplace"
// @Router /api/plugins/marketplaces/{id} [delete]
func (h *Handlers) RemoveMarketplace(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	if err := svc.RemoveMarketplace(c.Request().Context(), a, c.Param("id")); err != nil {
		return fail(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// RefreshMarketplace reads a marketplace now.
// @Summary Refresh a plugin marketplace
// @Description Reads the marketplace now. A failure keeps the last good list and records a short code in lastError; the answer is the marketplace in either case. A refresh never installs anything. Administrators only.
// @Tags Plugins
// @Produce json
// @Param id path string true "Marketplace ID"
// @Success 200 {object} Marketplace "The marketplace after the refresh"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 404 {object} schema.ErrorResponse "No such marketplace"
// @Router /api/plugins/marketplaces/{id}/refresh [post]
func (h *Handlers) RefreshMarketplace(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	m, err := svc.RefreshMarketplace(c.Request().Context(), a, c.Param("id"))
	if err != nil && m.ID == "" {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, marketplaceOf(m))
}

// Catalog lists the plugins of the active marketplaces.
// @Summary Browse the plugins
// @Description Lists the entries of the active marketplaces from their last good list; nothing is fetched. runs_code keeps the entries known to run code (true) or known to run none (false); an entry is known once a version of it was installed, and inspect tells for sure. Needs the plugins feature.
// @Tags Plugins
// @Produce json
// @Param q query string false "Text in the name, description, category or tags"
// @Param marketplace query string false "Marketplace ID"
// @Param category query string false "Category"
// @Param runs_code query bool false "Known to run code, or known to run none"
// @Param offset query int false "First entry"
// @Param limit query int false "Entries per page (default 50, at most 200)"
// @Success 200 {object} plugins.CatalogPage "One page"
// @Failure 400 {object} schema.ErrorResponse "Invalid query"
// @Router /api/plugins/catalog [get]
func (h *Handlers) Catalog(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	q := plugins.CatalogQuery{Q: c.QueryParam("q"), Marketplace: c.QueryParam("marketplace"), Category: c.QueryParam("category")}
	if v := c.QueryParam("runs_code"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return apiError(c, http.StatusBadRequest, ErrTypeInvalid, "runs_code must be true or false")
		}
		q.RunsCode = &b
	}
	for name, dst := range map[string]*int{"offset": &q.Offset, "limit": &q.Limit} {
		if v := c.QueryParam(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return apiError(c, http.StatusBadRequest, ErrTypeInvalid, name+" must be a number")
			}
			*dst = n
		}
	}
	page, err := svc.Catalog(c.Request().Context(), a, q)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, page)
}

// CatalogEntry returns one entry of a marketplace.
// @Summary One plugin of a marketplace
// @Description Returns one entry of an active marketplace, with the installs of it that the caller can see. Needs the plugins feature.
// @Tags Plugins
// @Produce json
// @Param marketplace path string true "Marketplace ID"
// @Param name path string true "Plugin name"
// @Success 200 {object} plugins.CatalogItem "The entry"
// @Failure 404 {object} schema.ErrorResponse "No such entry or marketplace"
// @Router /api/plugins/catalog/{marketplace}/{name} [get]
func (h *Handlers) CatalogEntry(c echo.Context) error {
	svc, a, ok := h.prepare(c)
	if !ok {
		return nil
	}
	it, err := svc.CatalogEntry(c.Request().Context(), a, c.Param("marketplace"), c.Param("name"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, it)
}
