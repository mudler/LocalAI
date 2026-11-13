package localai

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/remotemodel"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// ModelSearchEndpoint searches remote model providers (Hugging Face,
// Pirate Face) for models that can be installed.
//
//	@Summary	Search remote providers for installable models
//	@Description	Fans the query out to the chosen providers and returns the merged results plus one status per provider, so a provider that is down does not fail the search. Each result's `uri` can be passed to POST /models/import-uri. Results also say whether an installed model already comes from the repository (`installed`, `installed_name`), the backend an import would likely pick (`backend`, a hint derived from the tags) and whether the repository likely has several quantizations (`has_variants`). Use /api/models/remote/files for the exact files, sizes and backend of one result.
//	@Tags		models
//	@Param		q			query	string	true	"Search text"
//	@Param		providers	query	string	false	"Comma separated provider names (default: all)"
//	@Param		limit		query	int		false	"Results per provider (default 20, max 50)"
//	@Param		format		query	string	false	"File format filter (default gguf, any disables it)"
//	@Param		sort		query	string	false	"downloads (default), likes, updated or trending"
//	@Success	200	{object}	modelsearch.Response
//	@Failure	400	{object}	schema.ErrorResponse
//	@Router		/api/models/search [get]
func ModelSearchEndpoint(s *modelsearch.Searcher, cl *config.ModelConfigLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		bad := func(msg string) error {
			return c.JSON(http.StatusBadRequest, schema.ErrorResponse{
				Error: &schema.APIError{Message: msg, Code: http.StatusBadRequest, Type: "invalid_request_error"},
			})
		}
		q := strings.TrimSpace(c.QueryParam("q"))
		if q == "" {
			return bad("q is required")
		}
		limit := 0
		if v := c.QueryParam("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return bad("limit must be a positive integer")
			}
			limit = n
		}
		names := modelsearch.ParseNames(c.QueryParam("providers"))
		known := map[string]bool{}
		for _, n := range s.Names() {
			known[n] = true
		}
		for _, n := range names {
			if !known[n] {
				return bad("unknown provider " + strconv.Quote(n) + "; available: " + strings.Join(s.Names(), ", "))
			}
		}
		resp := s.Search(c.Request().Context(), modelsearch.Query{
			Text:   q,
			Limit:  limit,
			Format: c.QueryParam("format"),
			Sort:   c.QueryParam("sort"),
		}, names)
		remotemodel.EnrichSearch(&resp, installedIndex(cl))
		return c.JSON(http.StatusOK, resp)
	}
}
