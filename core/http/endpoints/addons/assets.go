// SPDX-License-Identifier: MIT
package addons

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

var assetDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// maxAssetPath bounds the file path of an asset request before the owner is
// asked. The manifest bounds declared paths well below it.
const maxAssetPath = 512

// GetAsset serves one declared file of an installed addon UI package.
// @Summary Read an immutable file of an addon UI package
// @Description Streams one file the addon manifest declares as UI code (the entry module or a style sheet) from the verified package on the owner, relayed through the receiving frontend. The digest in the path is the sha256 of the installed package, in lower-case hex, as listed in the ui entry of the apps list. The file is served only while the addon is installed and enabled and the digest is the current one; a file the manifest does not declare, a traversal, a directory, another addon, an older package, a disabled or uninstalled addon and a missing feature all give a denial without any file content, and a denial is never cached. The response is text/javascript or text/css with X-Content-Type-Options nosniff and Cache-Control private, max-age=31536000, immutable: the URL changes with the package, so the file never changes under it. The caller is authenticated by an API key header or by the same-origin browser session; a credential in the URL is never accepted. The addon process is never asked.
// @Tags Addons
// @Produce text/javascript
// @Produce text/css
// @Param id path string true "Addon ID"
// @Param digest path string true "Package digest, 64 lower-case hex characters"
// @Param path path string true "Declared package file, for example ui.js"
// @Success 200 {string} string "File bytes"
// @Header 200 {string} X-Content-Type-Options "nosniff"
// @Header 200 {string} Cache-Control "private, max-age=31536000, immutable"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, or the file, addon or package digest is not currently served"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/{id}/assets/{digest}/{path} [get]
func GetAsset(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		id, digest, name := c.Param("id"), c.Param("digest"), c.Param("*")
		if !appID.MatchString(id) || !assetDigest.MatchString(digest) || name == "" || len(name) > maxAssetPath {
			return failure(c, 403, "Addon request denied")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		var raw [16]byte
		if _, e = rand.Read(raw[:]); e != nil {
			return failure(c, 503, "Addons unavailable")
		}
		request := contract.Request{OperationID: "a" + hex.EncodeToString(raw[:]), AddonID: id, Action: "app-asset"}
		e = g.Relay(c.Request().Context(), a, gateway.RelayRequest{Request: request, Kind: "asset", Handle: name, Digest: digest}, c.Response())
		if e != nil {
			if c.Response().Committed {
				panic(http.ErrAbortHandler)
			}
			return resultError(c, e)
		}
		return nil
	}
}
