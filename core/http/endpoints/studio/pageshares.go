// SPDX-License-Identifier: MIT
package studio

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	addonsendpoint "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
)

// The routes with which a person shares one page of their own home. They have
// the guard of the folder sharing routes: a change, the list of grants and the
// user picker accept only the person's own browser session (a same-origin
// fetch, X-LocalAI-Confirm equal to the page name, or "pages" for the picker,
// no API key and no tool, agent or MCP marker). No MCP tool maps to them:
// sharing is never a tool. They call the folder sharing service; the grant is
// a grant on the page's folder with the page mark.

// pageSearchConfirm is the X-LocalAI-Confirm value of the user picker of the
// page routes, which has no page name.
const pageSearchConfirm = "pages"

var pageShareRules = []errRule{
	fixed(http.StatusForbidden, typeAuthz, "you cannot share this page", sharedfolders.ErrNotShareable),
	fixed(http.StatusConflict, "sharing_disabled", "pages cannot be shared on this server", sharedfolders.ErrSharingOff),
	fixed(http.StatusConflict, "sharing_disabled", "pages that people share are turned off by an administrator", sharedfolders.ErrPageSharesOff),
	verbatim(http.StatusBadRequest, typeInvalid, "shared folders: ", sharedfolders.ErrBadQuery, sharedfolders.ErrEveryoneEdit),
	fixed(http.StatusTooManyRequests, "rate_limited", "Too many searches for people; wait a moment and search again", sharedfolders.ErrSearchLimited),
}

func failPageShare(c echo.Context, err error) error {
	return mapErr(c, err, pageShareRules, failFolder)
}

// pageShareCall checks what every page sharing call needs: the pages feature,
// the files, the person's own browser session and the sharing of folders on
// this server. The browser session is checked first, as for folders.
func (h *Handlers) pageShareCall(c echo.Context, confirm string) (fileCall, bool) {
	if !addonsendpoint.HumanSession(c, confirm) {
		_ = fail(c, http.StatusForbidden, "not_human_surface", "This action requires your own browser session")
		return fileCall{}, false
	}
	fc, ok := h.pagesOn(c)
	if !ok {
		return fileCall{}, false
	}
	if !h.svc.SharedFolders().SharingAvailable() {
		_ = failPageShare(c, sharedfolders.ErrSharingOff)
		return fileCall{}, false
	}
	return fc, true
}

// pageShareJSON returns the answer of the share routes: the grants on a page
// with the consent of each named account to the current version.
func (h *Handlers) pageShareJSON(c echo.Context, fc fileCall, name string, f sharedfolders.Folder) (schema.PageShareList, error) {
	ctx := c.Request().Context()
	out := schema.PageShareList{Name: name, Enabled: h.svc.SharedFolders().PageSharesOn(), Grants: make([]schema.PageShareGrant, 0, len(f.Members))}
	var named []string
	for _, m := range f.Members {
		dn := m.Name
		if m.Principal == sharedfolders.Everyone {
			dn = ""
		} else {
			named = append(named, m.Principal)
		}
		out.Grants = append(out.Grants, schema.PageShareGrant{UserID: m.Principal, DisplayName: dn, Role: pages.RoleOf(m.Mode)})
	}
	p, err := h.pages.OwnShareState(ctx, fc.p, name)
	if err != nil {
		// A page without an index.html is no page now: its grants are still
		// listed, so the owner can take them back.
		return out, nil
	}
	out.Hash = p.Hash
	states, err := h.trust.ConsentStates(ctx, p.PageID, p.Hash, named)
	if err != nil {
		return out, err
	}
	for i, g := range out.Grants {
		if st, ok := states[g.UserID]; ok {
			out.Grants[i].Consent = st
			if st == pages.StateAllowed {
				out.AllowedCount++
			}
		}
	}
	return out, nil
}

// ListPageShares lists who a page of your home is shared with.
// @Summary List who a page of your home is shared with (owner, UI only).
// @Description Each grant has the account ID (or @everyone), the display name (empty for @everyone), the role view, use or edit, and, for a named account, whether it allowed the current version of the page (consent: none, allowed or changed). allowedCount is how many named accounts allowed the current version. enabled is false when an administrator turned off the pages that people share: the list still works and you can still take a share back. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the page name, no API key, no tool, agent or MCP marker). A page that is not yours and a page that does not exist get the same 403. No MCP tool maps to this route.
// @Tags pages
// @Param name path string true "page name"
// @Success 200 {object} schema.PageShareList "The grants"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session, a page you cannot share, or no such page"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled: pages cannot be shared on this server"
// @Router /api/chats/pages/{name}/shares [get]
func (h *Handlers) ListPageShares(c echo.Context) error {
	fc, ok := h.pageShareCall(c, c.Param("name"))
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().PageGrants(c.Request().Context(), fc.a, c.Param("name"))
	if err != nil {
		return failPageShare(c, err)
	}
	out, err := h.pageShareJSON(c, fc, c.Param("name"), f)
	if err != nil {
		return failPage(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

// SetPageShare shares a page of your home with a person or with everyone.
// @Summary Share a page of your home, or change the role (owner, UI only).
// @Description principal is the account ID that the user picker returned, an active named account, or @everyone for every account, the ones created later too. role is view (run the page and read its data), use (also save data files through the page) or edit (also change its code, in Files and in the page). edit is for named accounts only: @everyone with edit gets 400. The page stays where it is, in your home, and everybody sees the same data. People run the page's code in their own browser with their own rights, after they allow that version; a change of the code asks them again. A page that people share never runs on Home or in a slot by itself. Lowering a role or removing a grant drops the consent of that account and holds from its next call of the page on every frontend. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the page name, no API key, no tool, agent or MCP marker). Safe to repeat. A page that is not yours and a page that does not exist get the same 403. No MCP tool maps to this route: sharing is never a tool.
// @Tags pages
// @Param name path string true "page name"
// @Param request body schema.PageShareRequest true "The account and the role"
// @Success 200 {object} schema.PageShareList "The grants after the change"
// @Failure 400 {object} schema.ErrorResponse "A role that is not view, use or edit, edit for @everyone, or a principal that is not an active account"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session, a page you cannot share, or no such page"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled (pages cannot be shared here, or an administrator turned page sharing off), or a limit"
// @Router /api/chats/pages/{name}/shares [put]
func (h *Handlers) SetPageShare(c echo.Context) error {
	fc, ok := h.pageShareCall(c, c.Param("name"))
	if !ok {
		return nil
	}
	req, ok := bind[schema.PageShareRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	mode, ok := sharedfolders.RoleMode(req.Role)
	if !ok {
		return fail(c, http.StatusBadRequest, typeInvalid, "role is view, use or edit")
	}
	f, err := h.svc.SharedFolders().PageSetMember(c.Request().Context(), fc.a, c.Param("name"), req.Principal, mode)
	if err != nil {
		return failPageShare(c, err)
	}
	out, err := h.pageShareJSON(c, fc, c.Param("name"), f)
	if err != nil {
		return failPage(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

// RevokePageShare stops sharing a page of your home with a person or with everyone.
// @Summary Stop sharing a page of your home (owner, UI only).
// @Description Removes the grant of the principal, an account ID or @everyone, and drops the consent of that account. A person's own grant and the grant to everyone are separate. The grantee loses the page at their next call of it on every frontend, and the page they have open stops at that call. What they saved in the page's folder stays. It works while an administrator keeps page sharing off. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the page name, no API key, no tool, agent or MCP marker). Safe to repeat. No MCP tool maps to this route.
// @Tags pages
// @Param name path string true "page name"
// @Param principal path string true "Account ID, or @everyone"
// @Success 200 {object} schema.PageShareList "The grants after the change"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session, a page you cannot share, or no such page"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled: pages cannot be shared on this server"
// @Router /api/chats/pages/{name}/shares/{principal} [delete]
func (h *Handlers) RevokePageShare(c echo.Context) error {
	fc, ok := h.pageShareCall(c, c.Param("name"))
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().PageRemoveMember(c.Request().Context(), fc.a, c.Param("name"), memberParam(c))
	if err != nil {
		return failPageShare(c, err)
	}
	out, err := h.pageShareJSON(c, fc, c.Param("name"), f)
	if err != nil {
		return failPage(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

// SearchPageShareUsers is the user picker of the page routes.
// @Summary Search the people you can share a page with (owner, UI only).
// @Description The same picker as the other kinds of sharing: active named accounts by a prefix of the display name, 3 to 64 characters, at most 20 results, never you. A prefix of an address finds nobody; an account without a display name is found by its whole address only and comes back with an empty display name. Each result is an account ID and a display name and nothing else. Each person may search 20 times at once and then once every 2 seconds; a search over that rate answers 429 rate_limited. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the word pages, no API key, no tool, agent or MCP marker). No MCP tool maps to this route.
// @Tags pages
// @Param q query string true "Display name prefix, or a whole address"
// @Param limit query int false "At most this many results, up to 20"
// @Success 200 {object} schema.FolderShareUserList "Matching people"
// @Failure 400 {object} schema.ErrorResponse "The search text is not 3 to 64 characters"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled (pages cannot be shared here, or an administrator turned page sharing off)"
// @Failure 429 {object} schema.ErrorResponse "Type rate_limited: too many searches"
// @Router /api/chats/pages/shares/search [get]
func (h *Handlers) SearchPageShareUsers(c echo.Context) error {
	fc, ok := h.pageShareCall(c, pageSearchConfirm)
	if !ok {
		return nil
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	users, err := h.svc.SharedFolders().PageSearchAccounts(c.Request().Context(), fc.a, c.QueryParam("q"), limit)
	if err != nil {
		return failPageShare(c, err)
	}
	out := schema.FolderShareUserList{Users: make([]schema.FolderShareUser, 0, len(users))}
	for _, u := range users {
		out.Users = append(out.Users, schema.FolderShareUser{ID: u.ID, DisplayName: u.Name})
	}
	return c.JSON(http.StatusOK, out)
}
