package localai

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/remotemodel"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// searchUser is the person a search runs for: the signed-in user, or empty when
// authentication is off.
func searchUser(c echo.Context) string {
	if u := auth.GetUser(c); u != nil {
		return u.ID
	}
	return ""
}

// parseSourceFilters reads the filters.<source>.<id>=value parameters.
func parseSourceFilters(c echo.Context) (map[string]map[string]string, error) {
	var out map[string]map[string]string
	for key, vals := range c.QueryParams() {
		rest, ok := strings.CutPrefix(key, "filters.")
		if !ok {
			continue
		}
		source, id, ok := strings.Cut(rest, ".")
		if !ok || source == "" || id == "" || len(vals) != 1 {
			return nil, errors.New("a filter parameter is filters.<source>.<filter id>=<value>, given once")
		}
		if out == nil {
			out = map[string]map[string]string{}
		}
		if out[source] == nil {
			out[source] = map[string]string{}
		}
		out[source][id] = vals[0]
	}
	return out, nil
}

// ModelSearchEndpoint searches remote model sources (Hugging Face and the
// sources that add-ons provide) for models that can be installed.
//
//	@Summary	Search remote sources for installable models
//	@Description	Fans the query out to the sources and returns the merged results plus one status per source, so a source that is down does not fail the search. Without `providers` the sources are the caller's enabled set (every source the caller can see that they did not turn off; see /api/models/search/sources). A source can come from an add-on that provides the model-search slot. `filters.<source>.<id>=value` sets a filter that the source declares in `sources` of the answer; an unknown filter or a value that does not fit its type gives a 400 that names the filter. Each result's `uri` can be passed to POST /models/import-uri. Results also say whether an installed model already comes from the repository (`installed`, `installed_name`), the backend an import would likely pick (`backend`, a hint derived from the tags) and whether the repository likely has several quantizations (`has_variants`). Use /api/models/remote/files for the exact files, sizes and backend of one result. With sort=latest the search text may be empty: the answer is the newest models of each source that can list them. A source that cannot is not called and its status says `unsupported`. A status has `dropped`, the number of rows the host refused because of a scheme or a size it does not accept.
//	@Tags		models
//	@Param		q			query	string	false	"Search text. Required unless sort is latest"
//	@Param		providers	query	string	false	"Comma separated source IDs (default: the enabled set of the caller)"
//	@Param		limit		query	int		false	"Results per source (default 20, max 50)"
//	@Param		format		query	string	false	"File format filter (default gguf, any disables it)"
//	@Param		sort		query	string	false	"downloads (default), likes, updated, trending or latest (newest first; the only sort that works with no q)"
//	@Param		filters.source.id	query	string	false	"A filter of one source, for example filters.pirateface.min_seeders=2. The sources and their filters are listed in `sources` of the answer."
//	@Success	200	{object}	modelsearch.Response
//	@Failure	400	{object}	schema.ErrorResponse
//	@Router		/api/models/search [get]
func ModelSearchEndpoint(h *modelsearch.Host, cl *config.ModelConfigLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		bad := func(msg string) error {
			return c.JSON(http.StatusBadRequest, schema.ErrorResponse{
				Error: &schema.APIError{Message: msg, Code: http.StatusBadRequest, Type: "invalid_request_error"},
			})
		}
		user := searchUser(c)
		q := strings.TrimSpace(c.QueryParam("q"))
		sort := strings.ToLower(strings.TrimSpace(c.QueryParam("sort")))
		if q == "" && sort != modelsearch.SortLatest {
			return bad("q is required unless sort is latest")
		}
		limit := 0
		if v := c.QueryParam("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return bad("limit must be a positive integer")
			}
			limit = n
		}
		visible := h.Visible(c.Request().Context(), user)
		names := modelsearch.ParseNames(c.QueryParam("providers"))
		for _, n := range names {
			if !slices.Contains(visible, n) {
				return bad("unknown provider " + strconv.Quote(n) + "; available: " + strings.Join(visible, ", "))
			}
		}
		filters, err := parseSourceFilters(c)
		if err != nil {
			return bad(err.Error())
		}
		for source, values := range filters {
			if !slices.Contains(visible, source) {
				return bad("unknown provider " + strconv.Quote(source) + " in a filter; available: " + strings.Join(visible, ", "))
			}
			src, _ := h.Searcher.Source(source)
			if _, err := modelsearch.ValidateFilters(src, values); err != nil {
				return bad(err.Error())
			}
		}
		resp := h.Search(c.Request().Context(), user, modelsearch.Query{
			Text:    q,
			Limit:   limit,
			Format:  c.QueryParam("format"),
			Sort:    sort,
			Filters: filters,
		}, names)
		remotemodel.EnrichSearch(&resp, installedIndex(cl))
		return c.JSON(http.StatusOK, resp)
	}
}

// SearchSourcesRequest is the body of PUT /api/models/search/sources.
type SearchSourcesRequest struct {
	// Enabled is the new enabled set of source IDs. The host stores the
	// visible sources that are not in it as the ones turned off. An empty list
	// searches nothing.
	Enabled []string `json:"enabled"`
}

// ModelSearchSourcesEndpoint lists the sources and the enabled set.
//
//	@Summary	List the model search sources and the enabled set
//	@Description	Lists the sources that are installed (Hugging Face, and every add-on that provides the model-search slot and that the caller can see) with their titles, sorts and filters, and the enabled set of the caller: every source they can see that they did not turn off, so a source from a newly installed add-on is on with no action. `missing` is kept for older clients and is always empty.
//	@Tags		models
//	@Success	200	{object}	modelsearch.Overview
//	@Router		/api/models/search/sources [get]
func ModelSearchSourcesEndpoint(h *modelsearch.Host) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, h.Overview(c.Request().Context(), searchUser(c)))
	}
}

// ModelSearchSetSourcesEndpoint stores the enabled set.
//
//	@Summary	Set the enabled model search sources
//	@Description	Stores the set of sources the caller searches. The host stores what the caller turned off: every source they can see that is not in the list. An ID that is off and whose add-on is gone stays off, so installing the add-on again keeps the choice. The list has at most as many IDs as the visible sources, the stored off IDs and the built-in source, and never more than 64; a longer list gives a 400. It takes effect at once, with no restart. An ID must be a built-in source, a visible source, or already off. An empty list means nothing is searched.
//	@Tags		models
//	@Accept		json
//	@Param		request	body	localai.SearchSourcesRequest	true	"The enabled set"
//	@Success	200	{object}	modelsearch.Overview
//	@Failure	400	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/models/search/sources [put]
func ModelSearchSetSourcesEndpoint(h *modelsearch.Host) echo.HandlerFunc {
	return func(c echo.Context) error {
		fail := func(code int, msg string) error {
			return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: code, Type: "invalid_request_error"}})
		}
		var req SearchSourcesRequest
		if err := c.Bind(&req); err != nil || req.Enabled == nil {
			return fail(http.StatusBadRequest, "the body must be {\"enabled\": [source ids]}")
		}
		user := searchUser(c)
		if err := h.SetEnabled(c.Request().Context(), user, req.Enabled); err != nil {
			if errors.Is(err, modelsearch.ErrUnknownSource) || errors.Is(err, modelsearch.ErrTooManySources) {
				return fail(http.StatusBadRequest, err.Error())
			}
			return fail(http.StatusServiceUnavailable, "the choice of sources cannot be stored on this server")
		}
		return c.JSON(http.StatusOK, h.Overview(c.Request().Context(), user))
	}
}
