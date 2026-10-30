package studio

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The shared pages endpoints serve the pages of the folders an administrator
// shared with the caller. A shared page runs as the caller, so the UI runs it
// only after the caller allowed that version, and every call of its bridge
// says which version runs (X-LocalAI-Page-Hash) and, for a visit allowed once,
// the visit (X-LocalAI-Page-Visit). The server checks that consent on every
// call, with the caller's own grant on the folder.

const (
	headerPageHash  = "X-LocalAI-Page-Hash"
	headerPageVisit = "X-LocalAI-Page-Visit"
	// authorShared is the author of a shared page that another account wrote.
	authorShared = "other"
)

// sharedOn checks the pages feature, the files and the administrator's switch.
func (h *Handlers) sharedOn(c echo.Context) (fileCall, bool) {
	fc, ok := h.pagesOn(c)
	if !ok {
		return fc, false
	}
	if !h.svc.SharedPagesOn() {
		_ = fail(c, http.StatusForbidden, typeSharedPagesOff, "shared pages are off on this server; an administrator turned them off")
		return fileCall{}, false
	}
	return fc, true
}

// sharedRef reads the folder id and page name of the route. A folder id that
// is not a number is a page that does not exist.
func sharedRef(c echo.Context) (int64, string, error) {
	id, err := strconv.ParseInt(c.Param("folder"), 10, 64)
	if err != nil || id <= 0 {
		return 0, "", pages.ErrNotFound
	}
	return id, c.Param("name"), nil
}

func (h *Handlers) sharedJSON(c echo.Context, p pages.Page, consent string) schema.PagesPage {
	out := pageJSON(p)
	// Another account wrote it: "you" and "assistant" describe own pages only.
	if out.Author, out.UpdatedBy = authorShared, authorShared; p.AuthorID == userID(c) {
		out.Author, out.UpdatedBy = pages.AuthorYou, pages.AuthorYou
	}
	out.AuthorID, out.AuthorName = p.AuthorID, h.svc.UserName(c.Request().Context(), p.AuthorID)
	out.Folder, out.FolderID, out.Writable, out.Hash, out.MetaSize, out.Consent = p.Folder, p.FolderID, p.Writable, p.Hash, p.MetaSize, consent
	return out
}

// bridgeCheck checks the consent of the frame that calls: on failure it has
// written the response and returns false.
func (h *Handlers) bridgeCheck(c echo.Context, fc fileCall, folder int64, name string) bool {
	err := h.trust.Check(c.Request().Context(), fc.p, folder, name, c.Request().Header.Get(headerPageHash), c.Request().Header.Get(headerPageVisit))
	if err != nil {
		_ = failPage(c, err)
		return false
	}
	return true
}

// ListSharedPages lists the pages of the caller's shared folders.
// @Summary List the pages of your shared folders.
// @Description A shared folder is a folder outside your home that an administrator gave you access to. Its pages are the folders pages/<name>/ with an index.html. Each page has its folder, its author (the account that last wrote index.html), the hash of its code and your consent: none, allowed (this version) or changed (you allowed an earlier one). enabled is false when an administrator turned shared pages off.
// @Tags pages
// @Success 200 {object} schema.PagesSharedList "The pages, by folder and name"
// @Failure 403 {object} schema.ErrorResponse "The pages feature is off for your account"
// @Router /api/chats/shared-pages [get]
func (h *Handlers) ListSharedPages(c echo.Context) error {
	fc, ok := h.pagesOn(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	list, err := h.pages.ListShared(ctx, fc.p)
	if err != nil {
		return failPage(c, err)
	}
	out := schema.PagesSharedList{Enabled: h.svc.SharedPagesOn(), Pages: make([]schema.PagesPage, 0, len(list))}
	for _, p := range list {
		rev, err := h.trust.State(ctx, fc.p, p)
		if err != nil {
			return failPage(c, err)
		}
		out.Pages = append(out.Pages, h.sharedJSON(c, p, rev.State))
	}
	return c.JSON(http.StatusOK, out)
}

// GetSharedPage returns a shared page, its index.html and your consent.
// @Summary Read a shared page, its index.html and your consent.
// @Description The UI shows the trust sheet unless the consent is allowed, and runs the HTML only after you allow its hash. consent has the size and lines of the version you allowed, so a changed page can say what changed.
// @Tags pages
// @Param folder path integer true "shared folder id (folder_id of the list)"
// @Param name path string true "page name"
// @Success 200 {object} schema.PagesSharedSource "The page, its HTML and your consent"
// @Failure 403 {object} schema.ErrorResponse "Shared pages are off on this server"
// @Failure 404 {object} schema.ErrorResponse "No such page, or no access to its folder"
// @Router /api/chats/shared-pages/{folder}/{name} [get]
func (h *Handlers) GetSharedPage(c echo.Context) error {
	fc, ok := h.sharedOn(c)
	if !ok {
		return nil
	}
	folder, name, err := sharedRef(c)
	if err != nil {
		return failPage(c, err)
	}
	ctx := c.Request().Context()
	p, html, err := h.pages.SharedSource(ctx, fc.p, folder, name)
	if err != nil {
		return failPage(c, err)
	}
	rev, err := h.trust.State(ctx, fc.p, p)
	if err != nil {
		return failPage(c, err)
	}
	out := schema.PagesSharedSource{Page: h.sharedJSON(c, p, rev.State), HTML: html, Lines: strings.Count(html, "\n") + 1,
		Consent: schema.PagesConsent{State: rev.State}}
	if a := rev.Allowed; a != nil {
		out.Consent.AllowedAt, out.Consent.Size, out.Consent.MetaSize, out.Consent.Lines = utcPtr(a.At), a.Size, a.MetaSize, a.Lines
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, out)
}

// AllowSharedPage records your consent to a version of a shared page.
// @Summary Allow a version of a shared page to run.
// @Description hash is the hash you reviewed; a page that changed since is refused with 409. once allows one visit: the answer has a token that the bridge calls of that visit send. Each consent and revoke is audited.
// @Tags pages
// @Param folder path integer true "shared folder id"
// @Param name path string true "page name"
// @Param request body schema.PagesConsentRequest true "the version"
// @Success 200 {object} schema.PagesConsentResponse "The consent"
// @Failure 409 {object} schema.ErrorResponse "The page changed while you were reviewing it"
// @Router /api/chats/shared-pages/{folder}/{name}/consent [put]
func (h *Handlers) AllowSharedPage(c echo.Context) error {
	fc, ok := h.sharedOn(c)
	if !ok {
		return nil
	}
	folder, name, err := sharedRef(c)
	if err != nil {
		return failPage(c, err)
	}
	var req schema.PagesConsentRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	got, err := h.trust.Allow(c.Request().Context(), fc.p, folder, name, req.Hash, req.Once)
	if err != nil {
		return failPage(c, err)
	}
	out := schema.PagesConsentResponse{State: pages.StateAllowed, Kind: got.Kind, Hash: got.Hash, Token: got.Token}
	if got.Kind == pages.ConsentOnce {
		out.State, out.Expires = pages.StateNone, utcPtr(got.Expires)
	}
	return c.JSON(http.StatusOK, out)
}

// RevokeSharedPage removes your consents to a shared page.
// @Summary Stop allowing a shared page.
// @Description Its open frames stop at their next bridge call; the next open shows the trust sheet again.
// @Tags pages
// @Param folder path integer true "shared folder id"
// @Param name path string true "page name"
// @Success 204 "Revoked"
// @Router /api/chats/shared-pages/{folder}/{name}/consent [delete]
func (h *Handlers) RevokeSharedPage(c echo.Context) error {
	fc, ok := h.pagesOn(c) // a revoke works with shared pages off too
	if !ok {
		return nil
	}
	folder, name, err := sharedRef(c)
	if err != nil {
		return failPage(c, err)
	}
	if err := h.trust.Revoke(c.Request().Context(), fc.p, folder, name); err != nil {
		return failPage(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ListSharedPageFiles lists a folder of a shared page.
// @Summary List a folder of a shared page (bridge files.list).
// @Description Needs the headers X-LocalAI-Page-Hash (the version that runs) and, for a visit allowed once, X-LocalAI-Page-Visit.
// @Tags pages
// @Param folder path integer true "shared folder id"
// @Param name path string true "page name"
// @Param dir query string false "folder inside the page's folder"
// @Success 200 {object} schema.PagesFileList "The entries"
// @Failure 403 {object} schema.ErrorResponse "You have not allowed this version (page_not_allowed)"
// @Router /api/chats/shared-pages/{folder}/{name}/files [get]
func (h *Handlers) ListSharedPageFiles(c echo.Context) error {
	fc, ok := h.sharedOn(c)
	if !ok {
		return nil
	}
	folder, name, err := sharedRef(c)
	if err != nil {
		return failPage(c, err)
	}
	if !h.bridgeCheck(c, fc, folder, name) {
		return nil
	}
	list, err := h.pages.ListSharedFiles(c.Request().Context(), fc.p, folder, name, c.QueryParam("dir"))
	if err != nil {
		return failPage(c, err)
	}
	out := schema.PagesFileList{Entries: make([]schema.PagesFile, 0, len(list))}
	for _, f := range list {
		out.Entries = append(out.Entries, pageFileJSON(f))
	}
	return c.JSON(http.StatusOK, out)
}

// ReadSharedPageFile reads a text file of a shared page.
// @Summary Read a text file of a shared page (bridge files.read).
// @Tags pages
// @Param folder path integer true "shared folder id"
// @Param name path string true "page name"
// @Param path query string true "file inside the page's folder"
// @Success 200 {object} schema.PagesFileContent "The file"
// @Failure 403 {object} schema.ErrorResponse "You have not allowed this version (page_not_allowed)"
// @Router /api/chats/shared-pages/{folder}/{name}/file [get]
func (h *Handlers) ReadSharedPageFile(c echo.Context) error {
	fc, ok := h.sharedOn(c)
	if !ok {
		return nil
	}
	folder, name, err := sharedRef(c)
	if err != nil {
		return failPage(c, err)
	}
	if !h.bridgeCheck(c, fc, folder, name) {
		return nil
	}
	got, err := h.pages.ReadShared(c.Request().Context(), fc.p, folder, name, c.QueryParam("path"))
	if err != nil {
		return failPage(c, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.PagesFileContent{PagesFile: pageFileJSON(got.File), Content: got.Content})
}

// WriteSharedPageFile writes a text file of a shared page.
// @Summary Write a text file of a shared page (bridge files.write).
// @Description With your own grant on the folder: a read grant is refused with 403. A page cannot write its own index.html or page.json.
// @Tags pages
// @Param folder path integer true "shared folder id"
// @Param name path string true "page name"
// @Param request body schema.PagesWriteRequest true "the file"
// @Success 200 {object} schema.PagesFile "The stored file"
// @Failure 403 {object} schema.ErrorResponse "Not allowed, or you can only read the folder"
// @Router /api/chats/shared-pages/{folder}/{name}/file [put]
func (h *Handlers) WriteSharedPageFile(c echo.Context) error {
	fc, ok := h.sharedOn(c)
	if !ok {
		return nil
	}
	folder, name, err := sharedRef(c)
	if err != nil {
		return failPage(c, err)
	}
	if !h.bridgeCheck(c, fc, folder, name) {
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
	f, err := h.pages.WriteShared(c.Request().Context(), fc.p, folder, name, req.Path, []byte(req.Content), req.Base)
	if err != nil {
		return failPage(c, err)
	}
	return c.JSON(http.StatusOK, pageFileJSON(f))
}

// SendFromSharedPage sends a message from a shared page to one of your chats.
// @Summary Send a message from a shared page to a chat (bridge chat.send).
// @Description As for an own page: the UI calls it after you saw the message and chose Send, and the message says which shared page it comes from.
// @Tags pages
// @Param folder path integer true "shared folder id"
// @Param name path string true "page name"
// @Param request body schema.PagesChatRequest true "the message"
// @Success 200 {object} schema.PagesChatResponse "Where the message went"
// @Failure 403 {object} schema.ErrorResponse "You have not allowed this version (page_not_allowed)"
// @Failure 429 {object} schema.ErrorResponse "Too many messages"
// @Router /api/chats/shared-pages/{folder}/{name}/chat [post]
func (h *Handlers) SendFromSharedPage(c echo.Context) error {
	fc, ok := h.sharedOn(c)
	if !ok {
		return nil
	}
	folder, name, err := sharedRef(c)
	if err != nil {
		return failPage(c, err)
	}
	if !h.bridgeCheck(c, fc, folder, name) {
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
	ctx, user := c.Request().Context(), userID(c)
	page, err := h.pages.GetShared(ctx, fc.p, folder, name)
	if err != nil {
		return failPage(c, err)
	}
	if !h.pages.AllowSharedSend(fc.p, folder, name) {
		return failPage(c, pages.ErrRateLimited)
	}
	var rec session.ChatRecord
	if req.ChatID != "" {
		rec, err = h.svc.Get(ctx, user, req.ChatID)
	} else {
		rec, err = h.pageChat(c, user, "shared/"+strconv.FormatInt(folder, 10)+"/"+page.Name, page.Title, req.Model)
	}
	if err != nil {
		if errors.Is(err, errNoPageModel) {
			return fail(c, http.StatusBadRequest, typeInvalid, err.Error())
		}
		return failFor(c, err)
	}
	from := strings.TrimPrefix(page.Folder, "/") + "/pages/" + page.Name
	res, err := h.svc.Send(ctx, user, rec.ID, "From the shared page \""+page.Title+"\" ("+from+"):\n\n"+text)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.PagesChatResponse{ChatID: rec.ID, Title: rec.Title, Started: res.Started, PendingID: res.PendingID})
}
