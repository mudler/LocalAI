package studio

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	addonsendpoint "github.com/mudler/LocalAI/core/http/endpoints/addons"
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
	// maxPageErrorBody bounds the JSON body of an error report.
	maxPageErrorBody = 16 << 10
	// pageChatScan is how many recent chats are searched for a page's chat.
	pageChatScan = 200
)

var pageRules = []errRule{
	sentinelText(http.StatusBadRequest, typeInvalid, pages.ErrBadName),
	sentinelText(http.StatusBadRequest, typeInvalid, pages.ErrBadPath),
	fixed(http.StatusNotFound, typeNotFound, "page or file not found", pages.ErrNotFound),
	sentinelText(http.StatusRequestEntityTooLarge, typeInvalid, pages.ErrTooLarge),
	sentinelText(http.StatusUnsupportedMediaType, typeInvalid, pages.ErrNotText),
	sentinelText(http.StatusUnsupportedMediaType, typeInvalid, pages.ErrNotImage),
	verbatim(http.StatusForbidden, typeAuthz, "pages: ", pages.ErrReadOnly),
	sentinelText(http.StatusTooManyRequests, "rate_limit_error", pages.ErrRateLimited),
	verbatim(http.StatusForbidden, typeAuthz, "pages: ", pages.ErrFolderReadOnly),
	sentinelText(http.StatusBadRequest, typeInvalid, pages.ErrBadError),
	verbatim(http.StatusForbidden, typePageNotAllowed, "pages: ", pages.ErrNoConsent),
	verbatim(http.StatusConflict, typeConflict, "pages: ", pages.ErrChanged),
	verbatim(http.StatusConflict, typeConflict, "pages: ", pages.ErrCodeChanged),
	fixed(http.StatusForbidden, typeUserSharesOff, "pages that people share are turned off by an administrator", pages.ErrUserSharesOff),
	sentinelText(http.StatusBadRequest, typeInvalid, pages.ErrNoEmbed),
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
	out := schema.PagesPage{
		Name: p.Name, Path: p.Path, Title: p.Title, Icon: p.Icon, Description: p.Description,
		Source: p.Source, Author: p.Author, UpdatedAt: p.UpdatedAt, UpdatedBy: p.UpdatedBy, Rev: p.Rev, Size: p.Size,
		Widget: widgetJSON(p.Widget), OthersCanEdit: p.OthersCanEdit,
	}
	if p.OthersCanEdit {
		out.Hash, out.MetaSize, out.CodeFiles = p.Hash, p.MetaSize, append([]string{}, p.CodeFiles...)
	}
	return out
}

// guardedBridge checks the consent of the frame that calls the bridge of an own
// page that others may edit: it must carry the hash of a version that the
// owner allowed. A page nobody else may write needs none. On failure it has
// written the response and returns false.
func (h *Handlers) guardedBridge(c echo.Context, fc fileCall, name, rel string, read bool) bool {
	ctx := c.Request().Context()
	guarded, err := h.pages.Guarded(ctx, fc.p, name)
	if err != nil {
		_ = failPage(c, err)
		return false
	}
	if !guarded {
		return true
	}
	hash, visit := c.Request().Header.Get(headerPageHash), c.Request().Header.Get(headerPageVisit)
	if read {
		err = h.trust.OwnCheckRead(ctx, fc.p, name, hash, visit, rel)
	} else {
		err = h.trust.OwnCheck(ctx, fc.p, name, hash, visit)
	}
	if err != nil {
		_ = failPage(c, err)
		return false
	}
	return true
}

func widgetJSON(w *pages.Widget) *schema.PagesWidget {
	if w == nil {
		return nil
	}
	out := &schema.PagesWidget{Size: w.Size, Title: w.Title}
	if w.Compact != nil {
		out.Compact = &schema.PagesCompact{Width: w.Compact.Width}
	}
	return out
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
	out := schema.PagesList{Pages: make([]schema.PagesPage, 0, len(list)), PageAreaEnabled: h.svc.PageAreaOn()}
	// A person who cannot share (no accounts) has no counts; the list is the same.
	counts, _ := h.svc.SharedFolders().PageShareCounts(c.Request().Context(), fc.a)
	for _, p := range list {
		pj := pageJSON(p)
		if p.OthersCanEdit {
			rev, err := h.trust.State(c.Request().Context(), fc.p, p)
			if err != nil {
				return failPage(c, err)
			}
			pj.Consent = rev.State
		}
		pj.SharedWith = counts[p.Name]
		out.Pages = append(out.Pages, pj)
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
	ctx := c.Request().Context()
	out := schema.PagesSource{Page: pageJSON(p), HTML: html}
	if p.OthersCanEdit {
		rev, err := h.trust.State(ctx, fc.p, p)
		if err != nil {
			return failPage(c, err)
		}
		out.Page.Consent, out.Lines = rev.State, strings.Count(html, "\n")+1
		out.Consent = &schema.PagesConsent{State: rev.State, Embed: false}
		if a := rev.Allowed; a != nil {
			out.Consent.AllowedAt, out.Consent.Size, out.Consent.MetaSize, out.Consent.Lines = utcPtr(a.At), a.Size, a.MetaSize, a.Lines
		}
	}
	// How many of the accounts it is shared with allowed this version.
	if f, err := h.svc.SharedFolders().PageGrants(ctx, fc.a, p.Name); err == nil && len(f.Members) > 0 {
		if sh, err := h.pageShareJSON(c, fc, p.Name, f); err == nil {
			out.Page.SharedWith, out.Page.SharedAllowed = len(f.Members), sh.AllowedCount
			// The version that the grantees' consent is kept against: the page
			// compares it between two reads to say that they will be asked again.
			if out.Page.Hash == "" {
				out.Page.Hash = sh.Hash
			}
		}
	}
	return c.JSON(http.StatusOK, out)
}

// ownConsentCall checks what the consent routes of an own page need: your own
// browser session, as the share routes need, because the consent lets code
// that another account may have changed run as you. The session is checked
// first, then the pages feature.
func (h *Handlers) ownConsentCall(c echo.Context) (fileCall, bool) {
	if !addonsendpoint.HumanSession(c, c.Param("name")) {
		_ = fail(c, http.StatusForbidden, "not_human_surface", "This action requires your own browser session")
		return fileCall{}, false
	}
	return h.pagesOn(c)
}

// AllowOwnPage records your consent to a version of your own page that
// another account may edit.
// @Summary Allow a version of your own page that others can edit to run.
// @Description Only for a page with others_can_edit: someone else may change its code, so it runs for you only after you allow its hash, as a shared page does. hash is the hash you reviewed; a page that changed since is refused with 409. once allows one visit and returns a token for the bridge calls of that visit. embed is refused with 400: such a page never runs on Home, in a slot or in the dock by itself. A page nobody else may edit needs no consent and answers 404. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the page name, no API key, no tool, agent or MCP marker).
// @Tags pages
// @Param name path string true "page name"
// @Param request body schema.PagesConsentRequest true "the version"
// @Success 200 {object} schema.PagesConsentResponse "The consent"
// @Failure 403 {object} schema.ErrorResponse "Type not_human_surface: not your own browser session"
// @Failure 400 {object} schema.ErrorResponse "embed was asked for"
// @Failure 404 {object} schema.ErrorResponse "No such page, or it needs no consent"
// @Failure 409 {object} schema.ErrorResponse "The page changed while you were reviewing it"
// @Router /api/chats/pages/{name}/consent [put]
func (h *Handlers) AllowOwnPage(c echo.Context) error {
	fc, ok := h.ownConsentCall(c)
	if !ok {
		return nil
	}
	var req schema.PagesConsentRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	if req.Embed {
		return failPage(c, pages.ErrNoEmbed)
	}
	got, err := h.trust.OwnAllow(c.Request().Context(), fc.p, c.Param("name"), req.Hash, req.Once, false)
	if err != nil {
		return failPage(c, err)
	}
	out := schema.PagesConsentResponse{State: pages.StateAllowed, Kind: got.Kind, Hash: got.Hash, Token: got.Token}
	if got.Kind == pages.ConsentOnce {
		out.State, out.Expires = pages.StateNone, utcPtr(got.Expires)
	}
	return c.JSON(http.StatusOK, out)
}

// RevokeOwnPage removes your consents to your own page that others can edit.
// @Summary Stop allowing your own page that others can edit.
// @Description Its open frames stop at their next bridge call; the next open shows the trust sheet again. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the page name, no API key, no tool, agent or MCP marker).
// @Tags pages
// @Param name path string true "page name"
// @Failure 403 {object} schema.ErrorResponse "Type not_human_surface: not your own browser session"
// @Success 204 "Revoked"
// @Router /api/chats/pages/{name}/consent [delete]
func (h *Handlers) RevokeOwnPage(c echo.Context) error {
	fc, ok := h.ownConsentCall(c)
	if !ok {
		return nil
	}
	if err := h.trust.OwnRevoke(c.Request().Context(), fc.p, c.Param("name")); err != nil {
		return failPage(c, err)
	}
	return c.NoContent(http.StatusNoContent)
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
	if !h.guardedBridge(c, fc, c.Param("name"), "", false) {
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

// ReadPageFile reads a text file of a page, or an image with encoding=base64.
// @Summary Read a text file of a page (bridge files.read).
// @Description With encoding=base64 the file must be an image (png, jpg, gif, webp, avif, bmp, ico or svg, by extension) and content is its bytes in base64. The UI uses it to put the images next to a page into the page as data: URLs; the bridge of a page reads text only.
// @Tags pages
// @Param name path string true "page name"
// @Param path query string true "file inside the page's folder, for example data.json"
// @Param encoding query string false "base64 to read an image"
// @Success 200 {object} schema.PagesFileContent "The file and its text"
// @Failure 400 {object} schema.ErrorResponse "The path leaves the page's folder"
// @Failure 413 {object} schema.ErrorResponse "The file is larger than 1 MiB"
// @Failure 415 {object} schema.ErrorResponse "The file is not UTF-8 text, or not an image with encoding=base64"
// @Router /api/chats/pages/{name}/file [get]
func (h *Handlers) ReadPageFile(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	ctx, name, rel := c.Request().Context(), c.Param("name"), c.QueryParam("path")
	if !h.guardedBridge(c, fc, name, rel, true) {
		return nil
	}
	if image, ok := imageRead(c); !ok {
		return nil
	} else if image {
		img, err := h.pages.ReadImage(ctx, fc.p, name, rel)
		return sendPageImage(c, img, err)
	}
	got, err := h.pages.Read(ctx, fc.p, name, rel)
	if err != nil {
		return failPage(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.PagesFileContent{PagesFile: pageFileJSON(got.File), Content: got.Content})
}

// imageRead says whether a read asks for an image (encoding=base64). Any other
// value of encoding is refused, so a typo does not silently read text; then the
// response is written and ok is false.
func imageRead(c echo.Context) (image, ok bool) {
	if !c.QueryParams().Has("encoding") {
		return false, true
	}
	if c.QueryParam("encoding") != "base64" {
		_ = fail(c, http.StatusBadRequest, typeInvalid, "invalid params: encoding is base64 or absent")
		return false, false
	}
	return true, true
}

func sendPageImage(c echo.Context, img pages.Image, err error) error {
	if err != nil {
		return failPage(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.PagesFileContent{
		PagesFile: pageFileJSON(img.File), Content: base64.StdEncoding.EncodeToString(img.Data),
		Encoding: "base64", MediaType: img.MediaType,
	})
}

// WritePageFile writes a text file of a page.
// @Summary Write a text file of a page (bridge files.write).
// @Description Missing folders are created. A page writes data files only: code and document files (.js .mjs .cjs .css .html .htm .svg .xml .xhtml .wasm, index, page.json) are refused with 403; the Files page and file tools are not affected. With base, a file that changed since is kept and the content goes to a conflict copy (conflict_of). Writes are rate limited per page.
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
	if !h.guardedBridge(c, fc, c.Param("name"), "", false) {
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
	if !h.guardedBridge(c, fc, c.Param("name"), "", false) {
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

// ReportPageError keeps a runtime error of a page for the assistant to read.
// @Summary Report a runtime error of a page (bridge error capture).
// @Description The UI calls this for an error that the script of the caller's own page threw (window.onerror or an unhandled rejection), caught by the frame. Nothing is added to a chat; the assistant reads the errors with its page_errors tool. The texts are cut, the browser's own noise (such as ResizeObserver loop) is dropped, the same error is counted once, and a page keeps its 10 newest errors until any of its files changes. A page that reports too fast gets 429. Shared pages have no such route: only the owner's own view records.
// @Tags pages
// @Param name path string true "page name"
// @Param request body schema.PagesErrorRequest true "the error"
// @Success 200 {object} schema.PagesErrorResponse "recorded is false for noise"
// @Failure 400 {object} schema.ErrorResponse "No message"
// @Failure 404 {object} schema.ErrorResponse "No such page of yours"
// @Failure 429 {object} schema.ErrorResponse "Too many reports"
// @Router /api/chats/pages/{name}/errors [post]
func (h *Handlers) ReportPageError(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxPageErrorBody)
	var req schema.PagesErrorRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	// The text can hold the page's data: it is kept for the owner and never logged.
	recorded, err := h.pages.RecordError(c.Request().Context(), fc.p, c.Param("name"), pages.ErrorReport{
		Message: req.Message, Stack: req.Stack, File: req.File, Line: req.Line, Column: req.Column, Count: req.Count,
	})
	if err != nil {
		return failPage(c, err)
	}
	return c.JSON(http.StatusOK, schema.PagesErrorResponse{Recorded: recorded})
}
