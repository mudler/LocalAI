// SPDX-License-Identifier: MIT
package addons

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// privateHeaders are set before the owner is asked, so even an error response
// of an artifact route is never cached, sniffed or embedded. The owner adds
// the attachment headers of a successful download.
func privateHeaders(c echo.Context) {
	h := c.Response().Header()
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// GetArtifact returns the state of a private file result of the caller.
// @Summary Read the state of a private addon file result
// @Description Returns the caller's own artifact: its state (transferring, complete, failed or cancelled), the sanitized file name, the media type, the exact size and the expiry. Only the principal that requested the result can read it; an unknown identifier and another user's artifact give the same 403, so identifiers cannot be probed. The response never carries a storage path or an addon handle. This is the status lookup of the background transfer; it is read-only.
// @Tags Addons
// @Produce json
// @Param id path string true "Artifact ID (host generated, opaque)"
// @Success 200 {object} gateway.Artifact "Artifact state"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, or the artifact is unknown, expired or not the caller's"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/artifacts/{id} [get]
func GetArtifact(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		art, e := g.ArtifactStatus(c.Request().Context(), a, c.Param("id"))
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, art)
	}
}

// DownloadArtifact streams a complete private file result as an attachment.
// @Summary Download a private addon file result
// @Description Streams the caller's own complete artifact as an attachment, relayed through the receiving frontend from the owner that holds the bytes. Access needs the session or key of the principal that requested the result and, at the start and while streaming, current read access to every source resource and a current account. An artifact that is not complete, is expired, belongs to another principal or is unknown gives the same 403. The response is always an attachment (RFC 6266) with a sanitized name, a media type from a fixed allowlist or application/octet-stream, X-Content-Type-Options nosniff and Cache-Control private, no-store; it is never rendered inline. A failure after the first byte aborts the connection, so a truncated file never looks complete. Disabling the addon denies downloads and keeps the bytes until expiry; enabling it again serves unexpired artifacts to principals that still have access.
// @Tags Addons
// @Produce application/octet-stream
// @Produce text/csv
// @Produce application/json
// @Param id path string true "Artifact ID (host generated, opaque)"
// @Success 200 {string} string "Artifact bytes"
// @Header 200 {string} Content-Disposition "attachment with a sanitized filename and filename*"
// @Header 200 {string} X-Content-Type-Options "nosniff"
// @Header 200 {string} Cache-Control "private, no-store"
// @Header 200 {string} Content-Length "exact size of the artifact"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, or the artifact is unknown, expired, incomplete, not the caller's or no longer readable by the caller"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable, or the download bound is reached"
// @Router /api/addons/artifacts/{id}/download [get]
func DownloadArtifact(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		var raw [16]byte
		if _, e = rand.Read(raw[:]); e != nil {
			return failure(c, 503, "Addons unavailable")
		}
		// The relay needs an operation identifier of its own; the artifact is
		// named by the handle only.
		request := contract.Request{OperationID: "d" + hex.EncodeToString(raw[:]), Action: "artifact-download"}
		e = g.Relay(c.Request().Context(), a, gateway.RelayRequest{Request: request, Kind: "file", Handle: c.Param("id")}, c.Response())
		if e != nil {
			if c.Response().Committed {
				panic(http.ErrAbortHandler)
			}
			return resultError(c, e)
		}
		return nil
	}
}
