// SPDX-License-Identifier: MIT
package studio

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	addonsendpoint "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
)

// The routes with which a person shares a folder of their own home. Like the
// other sharing routes, a change, the list of grants and the user picker
// accept only the person's own browser session: a same-origin fetch with
// X-LocalAI-Confirm equal to the folder ID, no API key and no tool, agent or
// MCP marker. No MCP tool maps to them: sharing is never a tool.

var folderShareRules = []errRule{
	fixed(http.StatusForbidden, typeAuthz, "you cannot share this folder", sharedfolders.ErrNotShareable),
	fixed(http.StatusConflict, "sharing_disabled", "folders cannot be shared on this server", sharedfolders.ErrSharingOff),
	verbatim(http.StatusBadRequest, typeInvalid, "shared folders: ", sharedfolders.ErrBadQuery),
	fixed(http.StatusTooManyRequests, "rate_limited", "Too many searches for people; wait a moment and search again", sharedfolders.ErrSearchLimited),
}

func failFolderShare(c echo.Context, err error) error {
	return mapErr(c, err, folderShareRules, failFolder)
}

// shareCall checks what every sharing call needs: the files, the sharing of
// folders on this server and, for human, the person's own browser session.
// The browser session is checked first: a key, an agent or a tool learns
// nothing about the server from these routes, not even whether files are on.
func (h *Handlers) shareCall(c echo.Context, human bool) (fileCall, bool) {
	if human && !addonsendpoint.HumanSession(c, c.Param("id")) {
		_ = fail(c, http.StatusForbidden, "not_human_surface", "This action requires your own browser session")
		return fileCall{}, false
	}
	fc, ok := h.files(c)
	if !ok {
		return fileCall{}, false
	}
	if !h.svc.SharedFolders().SharingAvailable() {
		_ = failFolderShare(c, sharedfolders.ErrSharingOff)
		return fileCall{}, false
	}
	return fc, true
}

// ListFolderShares lists the folders of your home that are shared.
// @Summary List the folders of your home that you share.
// @Description sharing is false when folders cannot be shared on this server (local-owner mode, no accounts). Each folder has its path in your home, the number of grants and whether one of them is to every account. Read-only. No MCP tool maps to this route.
// @Tags chats
// @Success 200 {object} schema.FolderShareList "Your shared folders, by path"
// @Failure 409 {object} schema.ErrorResponse "Files are off on this server"
// @Router /api/chats/files/shares [get]
func (h *Handlers) ListFolderShares(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	sf := h.svc.SharedFolders()
	out := schema.FolderShareList{Sharing: sf.SharingAvailable(), Folders: []schema.FolderShare{}}
	if !out.Sharing {
		return c.JSON(http.StatusOK, out)
	}
	list, err := sf.Owned(c.Request().Context(), fc.a)
	if err != nil {
		return failFolderShare(c, err)
	}
	for _, f := range list {
		out.Folders = append(out.Folders, sharedfolders.ShareToSchema(f))
	}
	return c.JSON(http.StatusOK, out)
}

// FindFolderShare returns the folder of your home at a path, to share it.
// @Summary Find a folder of your home to share it.
// @Description Returns the folder's ID for the other sharing routes and how many it is shared with. A folder can be shared when it is inside your home, is not the home itself and is not LocalAI/ or Chats/ or inside them. Any other path gets 403, a missing one too. No MCP tool maps to this route.
// @Tags chats
// @Param request body schema.FolderSharePathRequest true "The folder's path in your home"
// @Success 200 {object} schema.FolderShare "The folder"
// @Failure 403 {object} schema.ErrorResponse "A folder you cannot share, or no such folder"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled, or files are off"
// @Router /api/chats/files/shares [post]
func (h *Handlers) FindFolderShare(c echo.Context) error {
	fc, ok := h.shareCall(c, false)
	if !ok {
		return nil
	}
	req, ok := bind[schema.FolderSharePathRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().OwnFolderAt(c.Request().Context(), fc.a, req.Path)
	if err != nil {
		return failFolderShare(c, err)
	}
	return c.JSON(http.StatusOK, sharedfolders.ShareToSchema(f))
}

func shareID(c echo.Context) int64 {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// ListFolderGrants lists who a folder of your home is shared with.
// @Summary List who a folder of your home is shared with (owner, UI only).
// @Description Each grant has the account ID (or @everyone), the display name (empty for @everyone) and the role read or write. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the folder ID, no API key, no tool, agent or MCP marker). A folder you cannot share and a missing one get the same 403. No MCP tool maps to this route.
// @Tags chats
// @Param id path integer true "Folder ID"
// @Success 200 {object} schema.FolderGrantList "The grants"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session, a folder you cannot share, or no such folder"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled"
// @Router /api/chats/files/shares/{id}/grants [get]
func (h *Handlers) ListFolderGrants(c echo.Context) error {
	fc, ok := h.shareCall(c, true)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().OwnGrants(c.Request().Context(), fc.a, shareID(c))
	if err != nil {
		return failFolderShare(c, err)
	}
	return c.JSON(http.StatusOK, sharedfolders.GrantsToSchema(f))
}

// SetFolderGrant shares a folder of your home with a person or with everyone.
// @Summary Share a folder of your home, or change the role (owner, UI only).
// @Description principal is the account ID that the user picker returned, an active named account, or @everyone for every account, the ones created later too. role is read or write; write lets them change and delete what is in the folder. Lowering write to read takes effect at their next file call on every frontend. Their running chats that hold the folder on the frontend that serves this call are stopped before the answer; the other frontends get the request on the chat bus and stop theirs within seconds. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the folder ID, no API key, no tool, agent or MCP marker). Safe to repeat. No MCP tool maps to this route: sharing is never a tool.
// @Tags chats
// @Param id path integer true "Folder ID"
// @Param principal path string true "Account ID, or @everyone"
// @Param request body schema.FolderGrantRequest true "The role"
// @Success 200 {object} schema.FolderGrantList "The grants after the change"
// @Failure 400 {object} schema.ErrorResponse "A role that is not read or write, or a principal that is not an active account"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session, a folder you cannot share, or no such folder"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled, or a limit"
// @Router /api/chats/files/shares/{id}/grants/{principal} [put]
func (h *Handlers) SetFolderGrant(c echo.Context) error {
	fc, ok := h.shareCall(c, true)
	if !ok {
		return nil
	}
	req, ok := bind[schema.FolderGrantRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().OwnSetMember(c.Request().Context(), fc.a, shareID(c), memberParam(c), sharedfolders.ParseMode(req.Role))
	if err != nil {
		return failFolderShare(c, err)
	}
	return c.JSON(http.StatusOK, sharedfolders.GrantsToSchema(f))
}

// RevokeFolderGrant stops sharing a folder of your home with a person or with everyone.
// @Summary Stop sharing a folder of your home (owner, UI only).
// @Description Removes the grant of the principal, an account ID or @everyone. A person's own grant and the grant to everyone are separate. The grantee loses the folder at their next file call on every frontend. Their running chats that hold it on the frontend that serves this call are stopped before the answer; the other frontends get the request on the chat bus and stop theirs within seconds. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the folder ID, no API key, no tool, agent or MCP marker). Safe to repeat. No MCP tool maps to this route.
// @Tags chats
// @Param id path integer true "Folder ID"
// @Param principal path string true "Account ID, or @everyone"
// @Success 200 {object} schema.FolderGrantList "The grants after the change"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session, a folder you cannot share, or no such folder"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled"
// @Router /api/chats/files/shares/{id}/grants/{principal} [delete]
func (h *Handlers) RevokeFolderGrant(c echo.Context) error {
	fc, ok := h.shareCall(c, true)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().OwnRemoveMember(c.Request().Context(), fc.a, shareID(c), memberParam(c))
	if err != nil {
		return failFolderShare(c, err)
	}
	return c.JSON(http.StatusOK, sharedfolders.GrantsToSchema(f))
}

// SearchFolderShareUsers is the user picker of a folder of your home.
// @Summary Search the people you can share a folder of your home with (owner, UI only).
// @Description The same picker as the other kinds of sharing: active named accounts by a prefix of the display name, 3 to 64 characters, at most 20 results, never you. A prefix of an address finds nobody; an account without a display name is found by its whole address only and comes back with an empty display name. Each result is an account ID and a display name and nothing else. Each person may search 20 times at once and then once every 2 seconds; a search over that rate answers 429 rate_limited. Accepted only from your own browser session (same-origin fetch, X-LocalAI-Confirm equal to the folder ID, no API key, no tool, agent or MCP marker). No MCP tool maps to this route.
// @Tags chats
// @Param id path integer true "Folder ID"
// @Param q query string true "Display name prefix, or a whole address"
// @Param limit query int false "At most this many results, up to 20"
// @Success 200 {object} schema.FolderShareUserList "Matching people"
// @Failure 400 {object} schema.ErrorResponse "The search text is not 3 to 64 characters"
// @Failure 403 {object} schema.ErrorResponse "Not your own browser session, a folder you cannot share, or no such folder"
// @Failure 409 {object} schema.ErrorResponse "Type sharing_disabled"
// @Failure 429 {object} schema.ErrorResponse "Type rate_limited: too many searches"
// @Router /api/chats/files/shares/{id}/users [get]
func (h *Handlers) SearchFolderShareUsers(c echo.Context) error {
	fc, ok := h.shareCall(c, true)
	if !ok {
		return nil
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	users, err := h.svc.SharedFolders().SearchAccounts(c.Request().Context(), fc.a, shareID(c), c.QueryParam("q"), limit)
	if err != nil {
		return failFolderShare(c, err)
	}
	out := schema.FolderShareUserList{Users: make([]schema.FolderShareUser, 0, len(users))}
	for _, u := range users {
		out.Users = append(out.Users, schema.FolderShareUser{ID: u.ID, DisplayName: u.Name})
	}
	return c.JSON(http.StatusOK, out)
}
