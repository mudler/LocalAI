package studio

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ChatFiles lists the files of a chat.
// @Summary List the files of a chat.
// @Description The files that belong to one conversation: the files in its folder under Chats (attached by the user, or written there), and the files its turns created or changed elsewhere in the user's home folder. Paths are the current ones, so they follow a rename of the chat. Each file has a kind: deliverable, or working for a script, a log or a draft the chat made on the way (in its work folder Chats/<chat>/work, or a helper file elsewhere). The model gets the same list in its prompt at every turn. A files event on the chat's event stream says that the list may have changed.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsChatFileList "The files of the chat"
// @Router /api/chats/{id}/files [get]
func (h *Handlers) ChatFiles(c echo.Context) error {
	cf, err := h.svc.ChatFiles(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsChatFileList{Files: make([]schema.ChatsChatFile, 0, len(cf.Files)), Folder: cf.Folder, Stale: cf.Stale}
	if cf.Folder != "" {
		out.FolderPath = homePath(strings.TrimSuffix(cf.Folder, "/"))
	}
	for _, f := range cf.Files {
		item := schema.ChatsChatFile{
			Name: f.Name, Path: homePath(f.GuestPath), GuestPath: f.GuestPath, Size: f.Size, Mime: f.Mime,
			Origin: string(f.Origin), Changed: f.Changed, Turn: f.Turn, Media: f.Media, Kind: f.Kind,
		}
		if !f.At.IsZero() {
			at := f.At.UTC()
			item.At = &at
		}
		out.Files = append(out.Files, item)
	}
	return c.JSON(http.StatusOK, out)
}

// homePath turns a guest path in the home mount into a path of the files API.
func homePath(guest string) string {
	return strings.TrimPrefix(guest, session.GuestRoot+"/"+session.GuestHome)
}
