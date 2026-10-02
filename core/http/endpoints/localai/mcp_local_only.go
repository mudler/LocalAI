// SPDX-License-Identifier: MIT

package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
)

// mcpLocalSessionsOnly answers the request itself when this frontend cannot
// serve it, and reports whether it did.
//
// MCP prompts and resources are served ONLY from MCP sessions this process
// holds. In distributed mode the frontend holds none: creating them is what an
// agent worker is for, because a stdio server usually means running docker, and
// nothing in this programme carries prompts or resources to one. That is a hole
// that PREDATES the removal of the message bus and is not closed by it; tools
// and discovery had a carrier and these two never did.
//
// Until this task the endpoints did not say so. A model with MCP servers
// configured answered 200 with an empty list, which reads as "this model has no
// prompts" and is a different statement from "this deployment cannot tell you".
// A client cannot act on the first and can act on the second, and an operator
// reading an empty list has no reason to look further. 501 with the reason in
// the body is the smallest honest answer, and it is deliberately NOT a 404 or
// an empty 200: the resource may well exist, on a worker this frontend has no
// verb to ask.
//
// One function for four endpoints, so the four cannot drift into four different
// stories about the same limitation. Each endpoint still has to CALL it, and
// each call is pinned by its own spec.
func mcpLocalSessionsOnly(c echo.Context, appConfig *config.ApplicationConfig, surface string) (bool, error) {
	if appConfig == nil || !appConfig.Distributed.Enabled {
		return false, nil
	}
	return true, c.JSON(http.StatusNotImplemented, map[string]string{
		"error": "MCP " + surface + " are not available in distributed mode: they are served only from MCP sessions held by this frontend, and in distributed mode the sessions live on an agent worker, which serves no " + surface + " verb",
	})
}
