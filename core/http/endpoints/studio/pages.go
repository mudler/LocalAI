package studio

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The pages endpoints serve the pages of the caller's own home and the bridge
// that the UI gives a page in its sandboxed frame. The UI calls them for the
// page; the page itself never reaches the server.

const (
	// maxPageMessage bounds a message that a page sends to a chat, in runes.
	maxPageMessage = 4000
	// maxPageWriteBody bounds the JSON body of a write: 1 MiB of content can
	// grow when JSON escapes it.
	maxPageWriteBody = 8 << 20
	// pageChatScan is how many recent chats are searched for a page's chat.
	pageChatScan = 200
)

var pageRules = []errRule{
	sentinelText(http.StatusBadRequest, typeInvalid, pages.ErrBadName),
	sentinelText(http.StatusBadRequest, typeInvalid, pages.ErrBadPath),
	fixed(http.StatusNotFound, typeNotFound, "page or file not found", pages.ErrNotFound),
	sentinelText(http.StatusRequestEntityTooLarge, typeInvalid, pages.ErrTooLarge),
	sentinelText(http.StatusUnsupportedMediaType, typeInvalid, pages.ErrNotText),
	sentinelText(http.StatusForbidden, typeAuthz, pages.ErrReadOnly),
	sentinelText(http.StatusTooManyRequests, "rate_limit_error", pages.ErrRateLimited),
	verbatim(http.StatusForbidden, typeAuthz, "pages: ", pages.ErrFolderReadOnly),
	verbatim(http.StatusForbidden, typePageNotAllowed, "pages: ", pages.ErrNoConsent),
	verbatim(http.StatusConflict, typeConflict, "pages: ", pages.ErrChanged),
}

func failPage(c echo.Context, err error) error {
	return mapErr(c, err, pageRules, failFile)
}

// pagesOn checks the pages feature and the files, as files does. On failure it
// has written the response and returns false.
func (h *Handlers) pagesOn(c echo.Context) (fileCall, bool) {
	if !auth.FeatureAllowed(c, auth.FeaturePages) {
		_ = fail(c, http.StatusForbidden, typeAuthz, "feature not enabled for your account: "+auth.FeaturePages)
		return fileCall{}, false
	}
	return h.files(c)
}

func pageJSON(p pages.Page) schema.PagesPage {
	return schema.PagesPage{
		Name: p.Name, Path: p.Path, Title: p.Title, Icon: p.Icon, Description: p.Description,
		Source: p.Source, Author: p.Author, UpdatedAt: p.UpdatedAt, UpdatedBy: p.UpdatedBy, Rev: p.Rev, Size: p.Size,
	}
}

func pageFileJSON(f pages.File) schema.PagesFile {
	return schema.PagesFile{Path: f.Path, Kind: f.Kind, Size: f.Size, Version: f.Version, ConflictOf: f.ConflictOf}
}

// ListPages lists the caller's pages.
// @Summary List your pages.
// @Description A page is a folder pages/<name>/ of your home with an index.html. Folders without one are left out.
// @Tags pages
// @Success 200 {object} schema.PagesList "Your pages, sorted by name"
// @Failure 403 {object} schema.ErrorResponse "The pages feature is off for your account"
// @Failure 409 {object} schema.ErrorResponse "Files are off on this server"
// @Router /api/chats/pages [get]
func (h *Handlers) ListPages(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	list, err := h.pages.List(c.Request().Context(), fc.p)
	if err != nil {
		return failPage(c, err)
	}
	out := schema.PagesList{Pages: make([]schema.PagesPage, 0, len(list))}
	for _, p := range list {
		out.Pages = append(out.Pages, pageJSON(p))
	}
	return c.JSON(http.StatusOK, out)
}

// GetPage returns a page with its index.html.
// @Summary Read a page and its index.html.
// @Description The UI shows the HTML in a sandboxed frame. An index.html over 1 MiB is refused.
// @Tags pages
// @Param name path string true "page name"
// @Success 200 {object} schema.PagesSource "The page and its HTML"
// @Failure 404 {object} schema.ErrorResponse "No such page"
// @Failure 413 {object} schema.ErrorResponse "index.html is larger than 1 MiB"
// @Router /api/chats/pages/{name} [get]
func (h *Handlers) GetPage(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	p, html, err := h.pages.Source(c.Request().Context(), fc.p, c.Param("name"))
	if err != nil {
		return failPage(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.PagesSource{Page: pageJSON(p), HTML: html})
}

// ListPageFiles lists a folder of a page.
// @Summary List a folder of a page (bridge files.list).
// @Tags pages
// @Param name path string true "page name"
// @Param dir query string false "folder inside the page's folder; default the page's folder"
// @Success 200 {object} schema.PagesFileList "The entries, with paths relative to the page's folder"
// @Failure 400 {object} schema.ErrorResponse "The path leaves the page's folder"
// @Router /api/chats/pages/{name}/files [get]
func (h *Handlers) ListPageFiles(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	list, err := h.pages.ListFiles(c.Request().Context(), fc.p, c.Param("name"), c.QueryParam("dir"))
	if err != nil {
		return failPage(c, err)
	}
	out := schema.PagesFileList{Entries: make([]schema.PagesFile, 0, len(list))}
	for _, f := range list {
		out.Entries = append(out.Entries, pageFileJSON(f))
	}
	return c.JSON(http.StatusOK, out)
}

// ReadPageFile reads a text file of a page.
// @Summary Read a text file of a page (bridge files.read).
// @Tags pages
// @Param name path string true "page name"
// @Param path query string true "file inside the page's folder, for example data.json"
// @Success 200 {object} schema.PagesFileContent "The file and its text"
// @Failure 400 {object} schema.ErrorResponse "The path leaves the page's folder"
// @Failure 413 {object} schema.ErrorResponse "The file is larger than 1 MiB"
// @Failure 415 {object} schema.ErrorResponse "The file is not UTF-8 text"
// @Router /api/chats/pages/{name}/file [get]
func (h *Handlers) ReadPageFile(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	got, err := h.pages.Read(c.Request().Context(), fc.p, c.Param("name"), c.QueryParam("path"))
	if err != nil {
		return failPage(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.PagesFileContent{PagesFile: pageFileJSON(got.File), Content: got.Content})
}

// WritePageFile writes a text file of a page.
// @Summary Write a text file of a page (bridge files.write).
// @Description Missing folders are created. A page cannot write its own index.html or page.json. With base, a file that changed since is kept and the content goes to a conflict copy (conflict_of). Writes are rate limited per page.
// @Tags pages
// @Param name path string true "page name"
// @Param request body schema.PagesWriteRequest true "the file"
// @Success 200 {object} schema.PagesFile "The stored file"
// @Failure 400 {object} schema.ErrorResponse "The path leaves the page's folder"
// @Failure 403 {object} schema.ErrorResponse "The page tried to change its own code"
// @Failure 413 {object} schema.ErrorResponse "The file is larger than 1 MiB"
// @Failure 429 {object} schema.ErrorResponse "Too many writes"
// @Router /api/chats/pages/{name}/file [put]
func (h *Handlers) WritePageFile(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxPageWriteBody)
	var req schema.PagesWriteRequest
	if err := c.Bind(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return failPage(c, pages.ErrTooLarge)
		}
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	if req.Base < 0 {
		return fail(c, http.StatusBadRequest, typeInvalid, "base must be a version number")
	}
	f, err := h.pages.Write(c.Request().Context(), fc.p, c.Param("name"), req.Path, []byte(req.Content), req.Base)
	if err != nil {
		return failPage(c, err)
	}
	return c.JSON(http.StatusOK, pageFileJSON(f))
}

// SendFromPage sends a message from a page to a chat.
// @Summary Send a message from a page to a chat (bridge chat.send).
// @Description The UI calls this after the person saw the message and chose Send. The message is added to the chat as a user message that says which page it comes from, so it shows in the chat before the assistant answers. Without chat_id it goes to the page's own chat, created on first use with model (or the model of your most recent chat). Rate limited per page.
// @Tags pages
// @Param name path string true "page name"
// @Param request body schema.PagesChatRequest true "the message"
// @Success 200 {object} schema.PagesChatResponse "Where the message went"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such page or chat"
// @Failure 429 {object} schema.ErrorResponse "Too many messages"
// @Router /api/chats/pages/{name}/chat [post]
func (h *Handlers) SendFromPage(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	var req schema.PagesChatRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	text := strings.TrimSpace(req.Text)
	if text == "" || utf8.RuneCountInString(text) > maxPageMessage {
		return fail(c, http.StatusBadRequest, typeInvalid, "text is required, at most 4000 characters")
	}
	ctx, name, user := c.Request().Context(), c.Param("name"), userID(c)
	page, err := h.pages.Get(ctx, fc.p, name)
	if err != nil {
		return failPage(c, err)
	}
	if !h.pages.AllowSend(fc.p, name) {
		return failPage(c, pages.ErrRateLimited)
	}
	var rec session.ChatRecord
	if req.ChatID != "" {
		rec, err = h.svc.Get(ctx, user, req.ChatID)
	} else {
		rec, err = h.pageChat(c, user, page.Name, page.Title, req.Model)
	}
	if err != nil {
		if errors.Is(err, errNoPageModel) {
			return fail(c, http.StatusBadRequest, typeInvalid, err.Error())
		}
		return failFor(c, err)
	}
	res, err := h.svc.Send(ctx, user, rec.ID, "From the page \""+page.Title+"\" (pages/"+page.Name+"):\n\n"+text)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.PagesChatResponse{ChatID: rec.ID, Title: rec.Title, Started: res.Started, PendingID: res.PendingID})
}

var errNoPageModel = errors.New("choose a model: there is no chat to take one from")

// pageChat finds the page's own chat among the recent chats, or creates it.
// originID names the page: its name for an own page.
func (h *Handlers) pageChat(c echo.Context, user, originID, title, model string) (session.ChatRecord, error) {
	ctx := c.Request().Context()
	recent, err := h.svc.ListChats(ctx, user, pageChatScan)
	if err != nil {
		return session.ChatRecord{}, err
	}
	for _, r := range recent {
		if r.Origin == session.OriginPage && r.OriginID == originID {
			return r, nil
		}
	}
	if model == "" {
		for _, r := range recent {
			if r.Model != "" {
				model = r.Model
				break
			}
		}
	}
	if model == "" {
		return session.ChatRecord{}, errNoPageModel
	}
	if h.models != nil {
		if err := h.models.Check(ctx, auth.GetUser(c), model); err != nil {
			return session.ChatRecord{}, errNoPageModel
		}
	}
	return h.svc.CreateAs(ctx, user, studiosvc.CreateInput{Title: "Page: " + title, Model: model},
		studiosvc.Origin{Kind: session.OriginPage, ID: originID})
}

// ListPageStarters lists the starter pages.
// @Summary List the starter pages that ship with the server.
// @Tags pages
// @Success 200 {object} schema.PagesStarterList "The starters"
// @Router /api/chats/page-starters [get]
func (h *Handlers) ListPageStarters(c echo.Context) error {
	if _, ok := h.pagesOn(c); !ok {
		return nil
	}
	list := h.pages.Starters()
	out := schema.PagesStarterList{Starters: make([]schema.PagesStarter, 0, len(list))}
	for _, s := range list {
		out.Starters = append(out.Starters, schema.PagesStarter{ID: s.ID, Title: s.Title, Icon: s.Icon, Description: s.Description})
	}
	return c.JSON(http.StatusOK, out)
}

// CopyPageStarter copies a starter page into your pages.
// @Summary Copy a starter page into pages/.
// @Description The copy is named after the starter, or the starter's name with -2, -3 and so on when that is taken. It is yours to change.
// @Tags pages
// @Param id path string true "starter id"
// @Success 201 {object} schema.PagesPage "The new page"
// @Failure 404 {object} schema.ErrorResponse "No such starter"
// @Router /api/chats/page-starters/{id}/copy [post]
func (h *Handlers) CopyPageStarter(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	p, err := h.pages.CopyStarter(c.Request().Context(), fc.p, c.Param("id"))
	if err != nil {
		return failPage(c, err)
	}
	return c.JSON(http.StatusCreated, pageJSON(p))
}
