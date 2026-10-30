package studio

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
)

// The shared folders endpoints. An administrator creates the folders
// /shared/<name>, chooses who may read or edit each one (a user or everyone),
// renames them and removes them with an undo. A user lists the folders shared
// with them; a folder without a grant is not found, like a missing one.

var folderRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "shared folder not found", sharedfolders.ErrNotFound),
	verbatim(http.StatusBadRequest, typeInvalid, "shared folders: ", sharedfolders.ErrBadName, sharedfolders.ErrBadMode,
		sharedfolders.ErrUnknownUser, sharedfolders.ErrBadRestore),
	verbatim(http.StatusConflict, typeConflict, "shared folders: ", sharedfolders.ErrExists, sharedfolders.ErrConflict, sharedfolders.ErrLimit),
	verbatim(http.StatusConflict, typeFilesOff, "shared folders: ", sharedfolders.ErrUnavailable),
}

func failFolder(c echo.Context, err error) error {
	return mapErr(c, err, folderRules, failFile)
}

func folderJSON(f sharedfolders.Folder, admin bool) schema.SharedFolder {
	return sharedfolders.ToSchema(f, admin)
}

func folderID(c echo.Context) int64 {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// by is the audit actor of an administrator's change.
func (h *Handlers) by(c echo.Context) string { return h.svc.Principal(userID(c)) }

// ListMySharedFolders lists the folders shared with the caller.
// @Summary List the folders shared with you.
// @Description A shared folder is a folder outside your home that an administrator gave you, or every user, access to. mode is read or write. pages counts its pages (pages/<name>/index.html); open them from /api/chats/shared-pages. Your chats see each folder at /workspace/shared-<name>.
// @Tags chats
// @Success 200 {object} schema.SharedFolderList "Your shared folders, by path"
// @Failure 409 {object} schema.ErrorResponse "Files are off on this server"
// @Router /api/chats/shared-folders [get]
func (h *Handlers) ListMySharedFolders(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	list, err := h.svc.SharedFolders().ForUser(c.Request().Context(), fc.p)
	if err != nil {
		return failFolder(c, err)
	}
	out := schema.SharedFolderList{Folders: make([]schema.SharedFolder, 0, len(list))}
	for _, f := range list {
		out.Folders = append(out.Folders, folderJSON(f, false))
	}
	return c.JSON(http.StatusOK, out)
}

// GetMySharedFolder returns one folder shared with the caller.
// @Summary Read a folder shared with you.
// @Tags chats
// @Param id path integer true "Shared folder ID"
// @Success 200 {object} schema.SharedFolder "The folder and your mode"
// @Failure 404 {object} schema.ErrorResponse "No such folder, or it is not shared with you"
// @Router /api/chats/shared-folders/{id} [get]
func (h *Handlers) GetMySharedFolder(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().GetForUser(c.Request().Context(), fc.p, folderID(c))
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusOK, folderJSON(f, false))
}

// AdminListSharedFolders lists every shared folder with its members.
// @Summary List the shared folders and their members (administrators).
// @Description A member is a user, or @everyone for every user, with the mode read or write.
// @Tags chats
// @Success 200 {object} schema.SharedFolderList "The shared folders, by name"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 409 {object} schema.ErrorResponse "Files are off on this server"
// @Router /api/chats/admin/shared-folders [get]
func (h *Handlers) AdminListSharedFolders(c echo.Context) error {
	list, err := h.svc.SharedFolders().List(c.Request().Context())
	if err != nil {
		return failFolder(c, err)
	}
	out := schema.SharedFolderList{Folders: make([]schema.SharedFolder, 0, len(list)),
		MaxFolders: sharedfolders.MaxFolders, MaxMembers: sharedfolders.MaxMembers}
	for _, f := range list {
		out.Folders = append(out.Folders, folderJSON(f, true))
	}
	return c.JSON(http.StatusOK, out)
}

// AdminCreateSharedFolder creates a shared folder.
// @Summary Create a shared folder (administrators).
// @Description Creates /shared/<name> with an empty pages folder. You become a member who can edit it, so you can fill it; remove yourself when you do not need it.
// @Tags chats
// @Param request body schema.SharedFolderNameRequest true "The folder's name"
// @Success 201 {object} schema.SharedFolder "The new folder"
// @Failure 400 {object} schema.ErrorResponse "A name that is not allowed"
// @Failure 409 {object} schema.ErrorResponse "The name is taken, or the limit is reached"
// @Router /api/chats/admin/shared-folders [post]
func (h *Handlers) AdminCreateSharedFolder(c echo.Context) error {
	req, ok := bind[schema.SharedFolderNameRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().Create(c.Request().Context(), h.by(c), req.Name)
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusCreated, folderJSON(f, true))
}

// AdminGetSharedFolder returns one shared folder with its members.
// @Summary Read a shared folder and its members (administrators).
// @Tags chats
// @Param id path integer true "Shared folder ID"
// @Success 200 {object} schema.SharedFolder "The folder"
// @Failure 404 {object} schema.ErrorResponse "No such folder"
// @Router /api/chats/admin/shared-folders/{id} [get]
func (h *Handlers) AdminGetSharedFolder(c echo.Context) error {
	f, err := h.svc.SharedFolders().Get(c.Request().Context(), folderID(c))
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusOK, folderJSON(f, true))
}

// AdminRenameSharedFolder renames a shared folder.
// @Summary Rename a shared folder (administrators).
// @Description The members, the pages and the consents to them stay. A chat that is open sees the new name the next time it opens.
// @Tags chats
// @Param id path integer true "Shared folder ID"
// @Param request body schema.SharedFolderNameRequest true "The new name"
// @Success 200 {object} schema.SharedFolder "The renamed folder"
// @Failure 404 {object} schema.ErrorResponse "No such folder"
// @Failure 409 {object} schema.ErrorResponse "The name is taken"
// @Router /api/chats/admin/shared-folders/{id} [patch]
func (h *Handlers) AdminRenameSharedFolder(c echo.Context) error {
	req, ok := bind[schema.SharedFolderNameRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().Rename(c.Request().Context(), h.by(c), folderID(c), req.Name)
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusOK, folderJSON(f, true))
}

// AdminDeleteSharedFolder removes a shared folder.
// @Summary Remove a shared folder (administrators).
// @Description Nothing is erased: the folder and its files go to the trash as one change, and undo_rev restores them with their members. Every user's consent to the folder's pages is dropped, so a restored page asks again.
// @Tags chats
// @Param id path integer true "Shared folder ID"
// @Success 200 {object} schema.SharedFolderRemoved "The removed folder and the change to undo"
// @Failure 404 {object} schema.ErrorResponse "No such folder"
// @Router /api/chats/admin/shared-folders/{id} [delete]
func (h *Handlers) AdminDeleteSharedFolder(c echo.Context) error {
	f, rev, err := h.svc.SharedFolders().Delete(c.Request().Context(), h.by(c), folderID(c))
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusOK, schema.SharedFolderRemoved{Folder: folderJSON(f, true), UndoRev: rev})
}

// AdminRestoreSharedFolder undoes the removal of a shared folder.
// @Summary Restore a removed shared folder (administrators).
// @Tags chats
// @Param request body schema.SharedFolderRestoreRequest true "The undo_rev of the removal"
// @Success 200 {object} schema.SharedFolder "The restored folder"
// @Failure 400 {object} schema.ErrorResponse "That change is not the removal of a shared folder"
// @Failure 409 {object} schema.ErrorResponse "A folder with its name exists now"
// @Router /api/chats/admin/shared-folders/restore [post]
func (h *Handlers) AdminRestoreSharedFolder(c echo.Context) error {
	req, ok := bind[schema.SharedFolderRestoreRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().Restore(c.Request().Context(), h.by(c), req.Rev)
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusOK, folderJSON(f, true))
}

// AdminSetSharedFolderMember gives a member access to a shared folder.
// @Summary Add a member to a shared folder or change their mode (administrators).
// @Description principal is a user's ID, email or name, or @everyone for every user, the ones who sign up later too. mode is read or write.
// @Tags chats
// @Param id path integer true "Shared folder ID"
// @Param principal path string true "User ID, email or name, or @everyone"
// @Param request body schema.SharedFolderMemberRequest true "The mode"
// @Success 200 {object} schema.SharedFolder "The folder and its members"
// @Failure 400 {object} schema.ErrorResponse "No such user, or a mode that is not read or write"
// @Failure 404 {object} schema.ErrorResponse "No such folder"
// @Router /api/chats/admin/shared-folders/{id}/members/{principal} [put]
func (h *Handlers) AdminSetSharedFolderMember(c echo.Context) error {
	req, ok := bind[schema.SharedFolderMemberRequest](c, msgNotJSON)
	if !ok {
		return nil
	}
	f, err := h.svc.SharedFolders().SetMember(c.Request().Context(), h.by(c), folderID(c), c.Param("principal"), sharedfolders.ParseMode(req.Mode))
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusOK, folderJSON(f, true))
}

// AdminRemoveSharedFolderMember takes a member's access to a shared folder away.
// @Summary Remove a member from a shared folder (administrators).
// @Description The member's pages list and chats stop showing the folder. A member who is not there is not an error.
// @Tags chats
// @Param id path integer true "Shared folder ID"
// @Param principal path string true "User ID, email or name, or @everyone"
// @Success 200 {object} schema.SharedFolder "The folder and its members"
// @Failure 404 {object} schema.ErrorResponse "No such folder"
// @Router /api/chats/admin/shared-folders/{id}/members/{principal} [delete]
func (h *Handlers) AdminRemoveSharedFolderMember(c echo.Context) error {
	f, err := h.svc.SharedFolders().RemoveMember(c.Request().Context(), h.by(c), folderID(c), c.Param("principal"))
	if err != nil {
		return failFolder(c, err)
	}
	return c.JSON(http.StatusOK, folderJSON(f, true))
}

// AdminSharedFolderEvents returns the audit of the shared folders.
// @Summary Read the audit of the shared folders (administrators).
// @Tags chats
// @Param limit query int false "How many lines, newest first (default 100, at most 500)"
// @Success 200 {object} schema.SharedFolderEventList "The newest changes first"
// @Router /api/chats/admin/shared-folders/events [get]
func (h *Handlers) AdminSharedFolderEvents(c echo.Context) error {
	ctx := c.Request().Context()
	evs, err := h.svc.SharedFolders().Events(ctx, intParam(c, "limit", 100, 500))
	if err != nil {
		return failFolder(c, err)
	}
	out := schema.SharedFolderEventList{Events: make([]schema.SharedFolderEvent, 0, len(evs))}
	name := func(id string) string { return h.svc.UserName(ctx, id) }
	for _, e := range evs {
		out.Events = append(out.Events, sharedfolders.EventToSchema(e, name))
	}
	return c.JSON(http.StatusOK, out)
}
