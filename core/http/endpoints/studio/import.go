package studio

import (
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/convert"
)

// maxBrowserImportBytes caps the upload of browser chats: the browser keeps them
// in localStorage, which holds a few megabytes.
const maxBrowserImportBytes = 64 << 20

// ImportBrowserChats stores the chats the old chat page kept in the browser.
// @Summary Import the chats that the old chat page kept in your browser.
// @Description Send the value of the browser's localStorage key "localai_chats_data" as it is: {"chats": [...]}. Each chat with a creation date and a message becomes one of your chats, with its transcript. A chat that you have already, with the same title and creation time, is skipped, so the upload can be repeated. The import changes nothing in the browser; the page clears its copy when the answer says nothing failed.
// @Tags chats
// @Accept json
// @Produce json
// @Param request body object true "the stored value, {chats: [...]}"
// @Success 200 {object} schema.ChatsBrowserImport "What was imported"
// @Failure 400 {object} schema.ErrorResponse "The body is not the stored form"
// @Router /api/chats/import/browser [post]
func (h *Handlers) ImportBrowserChats(c echo.Context) error {
	body, err := io.ReadAll(http.MaxBytesReader(c.Response(), c.Request().Body, maxBrowserImportBytes))
	if err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body could not be read")
	}
	res, err := h.svc.ImportBrowserChats(c.Request().Context(), userID(c), body)
	if errors.Is(err, convert.ErrBrowserForm) {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not the chat store of the old page")
	}
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsBrowserImport{Imported: res.Imported, Skipped: res.Skipped, Failed: res.Failed})
}
