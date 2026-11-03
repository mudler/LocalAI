// SPDX-License-Identifier: MIT
package addons

import (
	"errors"
	"io"
	"mime"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// The file routes stage an add-on zip on the owner. Staging runs nothing and
// changes no add-on, so an administrator's API key may use them, which lets
// `local-ai addons install` hand a file to a running server. The install that
// follows is the host access install: it needs the administrator's own browser
// session, the preview nonce and the digest of the file, so no script can
// install a staged file by itself. A file is bound to the user that uploaded
// it and expires after an hour.

// UploadAddonFile stages an add-on zip file.
// @Summary Upload an add-on zip file to install from a file (administrators)
// @Description Administrators only (an administrator's session or API key). Multipart form with one part named file, the add-on zip made by `make addons-zip`. The bytes stream to the add-on owner, which keeps them in a private staging area for one hour, bound to the uploading user, and verifies the file: its digest manifest, its paths and sizes, its platform and its add-on manifest. Nothing is installed. Returns the file ID, the sha256 of the file, the add-on ID, version, platform and API range, the host operations the manifest asks for, how the file relates to the installed version (new, upgrade, same or downgrade) and the origin "local file, unsigned": nothing signs a local file, so the administrator chooses what to install. The install is host-grants/preview and host-grants/install with sourceId local-file and version equal to the file ID; it needs the administrator's own browser session. Refusals carry the rule that failed as the error type: bad_zip, unsafe_path, unsupported_entry, too_large, too_many_files, compression_ratio, bad_manifest, digest_mismatch, wrong_platform or bad_mode. No MCP tool maps to this route.
// @Tags Addons
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "The add-on zip"
// @Success 200 {object} addons.FileView "The verified file"
// @Failure 400 {object} AddonErrorResponse "Not a multipart form with a file, or the file broke a rule"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or administration denied"
// @Failure 413 {object} AddonErrorResponse "The file is over the size limit"
// @Failure 429 {object} AddonErrorResponse "Too many staged files"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/files [post]
func UploadAddonFile(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, err, ok := hostGrantsAdmin(c, db, s)
		if !ok {
			return err
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		noteRequest(c, contract.Request{Action: "addon-file-upload"})
		mt, params, merr := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
		if merr != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
			return failure(c, 400, "Send the file as multipart form data")
		}
		mr, merr := c.Request().MultipartReader()
		if merr != nil {
			return failure(c, 400, "Send the file as multipart form data")
		}
		for {
			part, perr := mr.NextPart()
			if errors.Is(perr, io.EOF) {
				return failure(c, 400, "The form has no part named file")
			}
			if perr != nil {
				return failure(c, 400, "The form could not be read")
			}
			if part.FormName() != "file" {
				_ = part.Close()
				continue
			}
			view, uerr := g.UploadFile(c.Request().Context(), a, part.FileName(), part)
			_ = part.Close()
			if uerr != nil {
				return hostGrantsFailure(c, uerr)
			}
			return c.JSON(200, view)
		}
	}
}

// GetAddonFile reads a staged file.
// @Summary Read a staged add-on file (administrators)
// @Description Administrators only. The same answer as the upload, for a file of the calling user that has not expired. 404 when the file is gone or belongs to another user.
// @Tags Addons
// @Produce json
// @Param fileId path string true "File ID from the upload"
// @Success 200 {object} addons.FileView "The staged file"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or administration denied"
// @Failure 404 {object} AddonErrorResponse "No such file"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/files/{fileId} [get]
func GetAddonFile(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, err, ok := hostGrantsAdmin(c, db, s)
		if !ok {
			return err
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		noteRequest(c, contract.Request{Action: "addon-file-read"})
		view, e := g.DescribeFile(c.Request().Context(), a, c.Param("fileId"))
		if e != nil {
			return hostGrantsFailure(c, e)
		}
		return c.JSON(200, view)
	}
}

// DeleteAddonFile removes a staged file.
// @Summary Remove a staged add-on file (administrators)
// @Description Administrators only. Removes a file of the calling user that waits for an install. 404 when the file is gone or belongs to another user.
// @Tags Addons
// @Produce json
// @Param fileId path string true "File ID from the upload"
// @Success 204 "Removed"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or administration denied"
// @Failure 404 {object} AddonErrorResponse "No such file"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/files/{fileId} [delete]
func DeleteAddonFile(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, err, ok := hostGrantsAdmin(c, db, s)
		if !ok {
			return err
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		noteRequest(c, contract.Request{Action: "addon-file-delete"})
		if e := g.DropFile(c.Request().Context(), a, c.Param("fileId")); e != nil {
			return hostGrantsFailure(c, e)
		}
		return c.NoContent(204)
	}
}
