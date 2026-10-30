package studio

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
)

// The well-known files endpoints read and write the files of the user's home
// that change how LocalAI looks and behaves for that user. Every file has a
// JSON Schema; a read validates, a write is refused when the result breaks it.

// wellKnownRules maps the errors of the well-known files service. The other
// errors are the workspace's (fileRules).
var wellKnownRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "there is no such well-known file", wellknown.ErrUnknown),
	fixed(http.StatusBadRequest, typeInvalid, "path must name a .json file in your home", wellknown.ErrBadPath),
	fixed(http.StatusRequestEntityTooLarge, typeInvalid, "the file would be larger than 64 KiB", wellknown.ErrTooLarge),
	fixed(http.StatusConflict, typeConflict, "the history has no earlier valid version", wellknown.ErrNoPrevious),
}

func failWellKnown(c echo.Context, err error) error {
	var inv *wellknown.InvalidError
	if errors.As(err, &inv) {
		return c.JSON(http.StatusBadRequest, schema.ChatsWellKnownError{
			Error:  &schema.APIError{Message: "the file does not match its schema: " + strings.Join(inv.Errors, "; "), Code: http.StatusBadRequest, Type: typeInvalid},
			Errors: inv.Errors,
		})
	}
	var cf *wellknown.ConflictError
	if errors.As(err, &cf) {
		out := schema.ChatsWellKnownError{Error: &schema.APIError{Message: "the file changed since you read it", Code: http.StatusConflict, Type: typeConflict}}
		if cf.Current.Name != "" {
			cur := docOut(c, cf.Current)
			out.Current = &cur
		}
		return c.JSON(http.StatusConflict, out)
	}
	return mapErr(c, err, wellKnownRules, failFile)
}

// docOut is the answer for a document. user.json gets the account's name when
// it has none, marked as prefilled, so the settings page shows where it came from.
func docOut(c echo.Context, d wellknown.Doc) schema.ChatsWellKnownFile {
	out := schema.ChatsWellKnownFile{
		Name: string(d.Name), Path: d.Path, Exists: d.Exists, Version: d.Version, Rev: d.Rev, Data: d.Data,
		Valid: d.Valid, Errors: d.Errors, Fallback: d.Fallback, FallbackRev: d.FallbackRev,
	}
	if out.Data == nil {
		out.Data = map[string]any{}
	}
	if u := auth.GetUser(c); d.Name == wellknown.User && u != nil && strings.TrimSpace(u.Name) != "" {
		if name, _ := out.Data["name"].(string); name == "" {
			data := make(map[string]any, len(out.Data)+1)
			for k, v := range out.Data {
				data[k] = v
			}
			data["name"] = u.Name
			out.Data, out.Prefilled = data, map[string]string{"name": "account"}
		}
	}
	return out
}

// wellKnown resolves the name of the route. On failure it has answered.
func (h *Handlers) wellKnown(c echo.Context) (fileCall, wellknown.Name, bool) {
	fc, ok := h.files(c)
	if !ok {
		return fileCall{}, "", false
	}
	n, err := wellknown.Parse(c.Param("name"))
	if err != nil {
		_ = failWellKnown(c, err)
		return fileCall{}, "", false
	}
	return fc, n, true
}

// ifMatch reads the version the client read from If-Match ("12", W/"12" or 12).
// It is nil when the header is absent.
func ifMatch(c echo.Context) (*int64, bool) {
	v := strings.TrimSpace(c.Request().Header.Get("If-Match"))
	if v == "" {
		return nil, true
	}
	v = strings.Trim(strings.TrimPrefix(v, "W/"), `"`)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return nil, false
	}
	return &n, true
}

func (h *Handlers) answerDoc(c echo.Context, d wellknown.Doc) error {
	c.Response().Header().Set("ETag", strconv.Quote(strconv.FormatInt(d.Version, 10)))
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, docOut(c, d))
}

// GetWellKnown reads a well-known file.
// @Summary Read a well-known file of your home.
// @Description name is persona, user, shortcuts, home, ui or widget-data. The file is validated against its JSON Schema. When it is not valid, data holds its last valid version from the history and errors says what is wrong. A missing file is an empty object with exists false. widget-data needs path, the data file of a Home widget.
// @Tags chats
// @Param name path string true "persona, user, shortcuts, home, ui or widget-data"
// @Param path query string false "the data file, for widget-data"
// @Success 200 {object} schema.ChatsWellKnownFile "The file; the ETag header holds its version"
// @Failure 404 {object} schema.ErrorResponse "Not a well-known file"
// @Failure 409 {object} schema.ErrorResponse "Files are off on this server"
// @Router /api/chats/wellknown/{name} [get]
func (h *Handlers) GetWellKnown(c echo.Context) error {
	fc, n, ok := h.wellKnown(c)
	if !ok {
		return nil
	}
	d, err := h.svc.WellKnown().Read(c.Request().Context(), fc.p, n, c.QueryParam("path"))
	if err != nil {
		return failWellKnown(c, err)
	}
	return h.answerDoc(c, d)
}

// PutWellKnown changes a well-known file.
// @Summary Change a well-known file of your home.
// @Description The keys of values replace the keys of the file and a null removes one; keys the request does not name stay as they are. With replace, values is the whole file. The result must match the JSON Schema of the file and be at most 64 KiB. Send the version you read in If-Match (0 when the file did not exist): when the file changed since, the answer is 409 with what it holds now. Without If-Match the write is not checked.
// @Tags chats
// @Param name path string true "persona, user, shortcuts, home, ui or widget-data"
// @Param path query string false "the data file, for widget-data"
// @Param If-Match header string false "the version you read"
// @Param request body schema.ChatsWellKnownWrite true "the keys to change"
// @Success 200 {object} schema.ChatsWellKnownFile "The file as written"
// @Failure 400 {object} schema.ChatsWellKnownError "The result does not match the schema; errors lists why"
// @Failure 409 {object} schema.ChatsWellKnownError "The file changed since you read it; current holds it"
// @Failure 413 {object} schema.ErrorResponse "The file would be larger than 64 KiB"
// @Router /api/chats/wellknown/{name} [put]
func (h *Handlers) PutWellKnown(c echo.Context) error {
	fc, n, ok := h.wellKnown(c)
	if !ok {
		return nil
	}
	base, ok := ifMatch(c)
	if !ok {
		return fail(c, http.StatusBadRequest, typeInvalid, "If-Match must be a version number")
	}
	var req schema.ChatsWellKnownWrite
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	if req.Values == nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "values is required")
	}
	d, err := h.svc.WellKnown().Write(c.Request().Context(), fc.p, wellknown.WriteRequest{
		Name: n, Path: c.QueryParam("path"), Values: req.Values, Replace: req.Replace, Base: base,
	})
	if err != nil {
		return failWellKnown(c, err)
	}
	return h.answerDoc(c, d)
}

// RevertWellKnown goes back to the previous version of a well-known file.
// @Summary Revert a well-known file to its previous valid version.
// @Description Writes the newest earlier version that differs from the file and matches its schema, as a new change: the revert itself can be undone.
// @Tags chats
// @Param name path string true "persona, user, shortcuts, home, ui or widget-data"
// @Param path query string false "the data file, for widget-data"
// @Success 200 {object} schema.ChatsWellKnownFile "The file as written"
// @Failure 409 {object} schema.ErrorResponse "There is no earlier valid version"
// @Router /api/chats/wellknown/{name}/revert [post]
func (h *Handlers) RevertWellKnown(c echo.Context) error {
	fc, n, ok := h.wellKnown(c)
	if !ok {
		return nil
	}
	d, err := h.svc.WellKnown().Revert(c.Request().Context(), fc.p, n, c.QueryParam("path"))
	if err != nil {
		return failWellKnown(c, err)
	}
	return h.answerDoc(c, d)
}

// WellKnownSchema returns the JSON Schema of a well-known file.
// @Summary Get the JSON Schema of a well-known file.
// @Description The same schema is in the built-in skill localai-workspace, which the assistant reads. Properties may carry x-ui hints for the settings pages (widget, hidden).
// @Tags chats
// @Param name path string true "persona, user, shortcuts, home, ui or widget-data"
// @Produce application/schema+json
// @Success 200 {object} map[string]any "A JSON Schema, draft 2020-12"
// @Failure 404 {object} schema.ErrorResponse "Not a well-known file"
// @Router /api/chats/wellknown/{name}/schema [get]
func (h *Handlers) WellKnownSchema(c echo.Context) error {
	n, err := wellknown.Parse(c.Param("name"))
	if err != nil {
		return failWellKnown(c, err)
	}
	data, err := wellknown.Schema(n)
	if err != nil {
		return failWellKnown(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-cache")
	return c.Blob(http.StatusOK, "application/schema+json", data)
}
