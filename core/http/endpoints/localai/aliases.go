package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
)

// AliasInfo is one alias -> target pair.
type AliasInfo struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// ListAliasesEndpoint returns every configured model alias and its target.
//
//	@Summary	List model aliases
//	@Tags		models
//	@Success	200	{array}	AliasInfo
//	@Router		/api/aliases [get]
func ListAliasesEndpoint(cl *config.ModelConfigLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Non-nil so an empty result marshals as [] rather than null.
		out := []AliasInfo{}
		for _, cfg := range cl.GetAllModelsConfigs() {
			if cfg.IsAlias() {
				out = append(out, AliasInfo{Name: cfg.Name, Target: cfg.Alias})
			}
		}
		return c.JSON(http.StatusOK, out)
	}
}
