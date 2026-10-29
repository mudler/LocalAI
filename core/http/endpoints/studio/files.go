package studio

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The files endpoints work on the user's own workspace. Every path is inside
// the user's home folder, which is "/": a path cannot name anything else, and
// another user's files cannot be named at all.

// fileCall holds what a files endpoint needs after the checks.
type fileCall struct {
	f *files.Service
	p string // the user's owner ID
	a workspace.Actor
}

// files checks that the workspace is on and the user has a home. On failure it
// has written the response and returns false.
func (h *Handlers) files(c echo.Context) (fileCall, bool) {
	f := h.svc.Files()
	if !f.Available() {
		_ = fail(c, http.StatusConflict, "files_off", f.Unavailable())
		return fileCall{}, false
	}
	p := h.svc.Principal(userID(c))
	if err := f.EnsureHome(c.Request().Context(), p); err != nil {
		_ = failFile(c, err)
		return fileCall{}, false
	}
	return fileCall{f: f, p: p, a: f.Actor(p)}, true
}

// fileRules maps the errors of the workspace. A path the user cannot reach is
// the same answer as one that does not exist.
var fileRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "not found", workspace.ErrNotFound, workspace.ErrReadOnly, workspace.ErrNotAdmin),
	fixed(http.StatusForbidden, "forbidden", "only an administrator may change the shared skills", files.ErrNotAdmin),
	verbatim(http.StatusBadRequest, typeInvalid, "", files.ErrBadSkill),
	fixed(http.StatusBadRequest, typeInvalid, "the path has a name that is not allowed", workspace.ErrBadName),
	fixed(http.StatusConflict, typeConflict, "something with that name exists already", workspace.ErrExists),
	fixed(http.StatusRequestEntityTooLarge, typeInvalid, "the file is too large", workspace.ErrTooLarge),
	fixed(http.StatusBadRequest, typeInvalid, "that cannot be done to this path", workspace.ErrInvalid),
	fixed(http.StatusConflict, typeConflict, "later changes conflict with this undo", workspace.ErrConflict),
	verbatim(http.StatusConflict, typeFilesOff, "", files.ErrUnavailable),
}

// failFile maps a workspace error to a response.
func failFile(c echo.Context, err error) error {
	// The server's body limit fails the read of an upload with a 413 of its own.
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) && httpErr.Code == http.StatusRequestEntityTooLarge {
		err = workspace.ErrTooLarge
	}
	return mapErr(c, err, fileRules, func(c echo.Context, err error) error {
		xlog.Error("studio: files request failed", "path", c.Request().URL.Path, "error", err)
		return fail(c, http.StatusInternalServerError, typeServerError, "the request failed")
	})
}

func (fc fileCall) node(n workspace.Node) schema.ChatsFile {
	return schema.ChatsFile{
		Path: fc.f.Rel(fc.p, n.Path), Name: n.Name, Kind: string(n.Kind), Size: n.Size, Version: n.Version, Rev: n.Rev,
		ConflictOf: fc.f.Rel(fc.p, n.ConflictOf),
	}
}

// abs resolves a path of the request. "/" is the home, which no call may delete or move.
func (fc fileCall) abs(rel string, allowHome bool) (string, error) {
	if rel == "" {
		rel = "/"
	}
	abs, err := fc.f.Abs(fc.p, rel)
	if err != nil {
		return "", err
	}
	if !allowHome && fc.f.Rel(fc.p, abs) == "/" {
		return "", workspace.ErrInvalid
	}
	return abs, nil
}

// ListFiles lists a folder.
// @Summary List a folder of your files.
// @Tags chats
// @Param path query string false "folder inside your home; default /"
// @Success 200 {object} schema.ChatsFileList "The folder's entries, sorted by name"
// @Failure 409 {object} schema.ErrorResponse "Files are off on this server"
// @Router /api/chats/files [get]
func (h *Handlers) ListFiles(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	abs, err := fc.abs(c.QueryParam("path"), true)
	if err != nil {
		return failFile(c, err)
	}
	kids, err := fc.f.Store().List(c.Request().Context(), fc.a, abs)
	if err != nil {
		return failFile(c, err)
	}
	out := schema.ChatsFileList{Path: fc.f.Rel(fc.p, abs), Entries: make([]schema.ChatsFile, 0, len(kids))}
	for _, k := range kids {
		out.Entries = append(out.Entries, fc.node(k))
	}
	return c.JSON(http.StatusOK, out)
}

// inlineTypes may be shown by the browser. Everything else is a download, and
// is served as opaque bytes, so a file a user writes cannot run as a page.
var inlineTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true, "text/plain": true,
	"audio/mpeg": true, "audio/wav": true, "audio/ogg": true, "video/mp4": true, "video/webm": true,
}

// GetFileContent downloads a file.
// @Summary Download a file.
// @Description Files are served as downloads. With inline=1 an image, PDF, plain text, audio or video is shown by the browser instead. Ranges are supported.
// @Tags chats
// @Param path query string true "file inside your home"
// @Param inline query int false "1 to show a safe type in the browser"
// @Produce application/octet-stream
// @Success 200 {file} binary "The file"
// @Router /api/chats/files/content [get]
func (h *Handlers) GetFileContent(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	abs, err := fc.abs(c.QueryParam("path"), false)
	if err != nil {
		return failFile(c, err)
	}
	r, n, err := fc.f.Store().Open(c.Request().Context(), fc.a, abs)
	if err != nil {
		return failFile(c, err)
	}
	defer r.Close()
	ct := mime.TypeByExtension(path.Ext(n.Name))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if ct == "" {
		var err error
		if ct, err = sniffType(r); err != nil {
			return fail(c, http.StatusInternalServerError, "server_error", "the file could not be read")
		}
	}
	disposition := "attachment"
	if inlineTypes[ct] {
		if c.QueryParam("inline") == "1" {
			disposition = "inline"
		}
	} else {
		ct = "application/octet-stream"
	}
	res := c.Response()
	res.Header().Set("Content-Type", ct)
	res.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": n.Name}))
	res.Header().Set("X-Content-Type-Options", "nosniff")
	res.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(res, c.Request(), n.Name, time.Time{}, r)
	return nil
}

// sniffType detects the content type from the start of r and rewinds r. A
// reader that cannot rewind is an error: ServeContent would send the file from
// the wrong offset with status 200.
func sniffType(r io.ReadSeeker) (string, error) {
	head := make([]byte, 512)
	k, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	ct := http.DetectContentType(head[:k])
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return ct, nil
}

// PutFileContent uploads a file.
// @Summary Upload a file.
// @Description The request body is the file. A file that exists is replaced only when the upload says which version it started from (base); otherwise both are kept and the new one gets a conflict name, reported in conflict_of. overwrite=1 replaces without the check.
// @Tags chats
// @Param path query string true "file inside your home; missing folders are created"
// @Param base query int false "version of the existing file the upload started from"
// @Param overwrite query int false "1 to replace without the version check"
// @Accept application/octet-stream
// @Success 200 {object} schema.ChatsFile "The stored file"
// @Failure 413 {object} schema.ErrorResponse "The file is too large"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/files/content [put]
func (h *Handlers) PutFileContent(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	abs, err := fc.abs(c.QueryParam("path"), false)
	if err != nil {
		return failFile(c, err)
	}
	opts := workspace.PutOptions{Overwrite: c.QueryParam("overwrite") == "1"}
	if b := c.QueryParam("base"); b != "" {
		v, perr := strconv.ParseInt(b, 10, 64)
		if perr != nil || v < 0 {
			return fail(c, http.StatusBadRequest, "invalid_request_error", "base must be a version number")
		}
		opts.BaseVersion = v
	}
	n, err := fc.f.Store().Put(c.Request().Context(), fc.a, abs, c.Request().Body, opts)
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, fc.node(n))
}

// CreateFolder creates a folder.
// @Summary Create a folder.
// @Tags chats
// @Param request body schema.ChatsFolderRequest true "the folder"
// @Success 200 {object} schema.ChatsFile "The folder"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/files/folders [post]
func (h *Handlers) CreateFolder(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	var req schema.ChatsFolderRequest
	if err := c.Bind(&req); err != nil || req.Path == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "path is required")
	}
	abs, err := fc.abs(req.Path, false)
	if err != nil {
		return failFile(c, err)
	}
	n, err := fc.f.Store().Mkdir(c.Request().Context(), fc.a, abs)
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, fc.node(n))
}

// MoveFile moves or renames a file or folder.
// @Summary Move or rename a file or folder.
// @Tags chats
// @Param request body schema.ChatsMoveRequest true "from and to"
// @Success 200 {object} schema.ChatsFile "The moved entry"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/files/move [post]
func (h *Handlers) MoveFile(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	var req schema.ChatsMoveRequest
	if err := c.Bind(&req); err != nil || req.From == "" || req.To == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "from and to are required")
	}
	from, err := fc.abs(req.From, false)
	if err != nil {
		return failFile(c, err)
	}
	to, err := fc.abs(req.To, false)
	if err != nil {
		return failFile(c, err)
	}
	n, err := fc.f.Store().Move(c.Request().Context(), fc.a, from, to)
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, fc.node(n))
}

// DeleteFile moves a file or folder to the Trash.
// @Summary Delete a file or folder.
// @Description Nothing is erased: the deletion is a change that the Trash lists and undo restores.
// @Tags chats
// @Param path query string true "file or folder inside your home"
// @Success 200 {object} map[string]bool "deleted"
// @Router /api/chats/files [delete]
func (h *Handlers) DeleteFile(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	abs, err := fc.abs(c.QueryParam("path"), false)
	if err != nil {
		return failFile(c, err)
	}
	if err := fc.f.Store().Delete(c.Request().Context(), fc.a, abs); err != nil {
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"deleted": true})
}

// Trash lists deletions that can be undone.
// @Summary List the Trash.
// @Tags chats
// @Success 200 {object} schema.ChatsTrash "Deletions that nobody has undone, newest first"
// @Router /api/chats/files/trash [get]
func (h *Handlers) Trash(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	entries, err := fc.f.Trash(c.Request().Context(), fc.p, intParam(c, "limit", 100, 500))
	if err != nil {
		return failFile(c, err)
	}
	out := schema.ChatsTrash{Entries: make([]schema.ChatsTrashEntry, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, schema.ChatsTrashEntry{Rev: e.Rev, Title: e.Title, At: e.At, Paths: e.Paths})
	}
	return c.JSON(http.StatusOK, out)
}

// UndoFiles undoes a change set.
// @Summary Undo a change.
// @Description Restores what a change set changed, as a new change. When later changes conflict, nothing changes and the answer is 409, unless skip_conflicts is set: then the rest is restored and the conflicts are listed.
// @Tags chats
// @Param request body schema.ChatsUndoRequest true "the revision"
// @Success 200 {object} schema.ChatsUndoResponse "The new revision and any conflicts that were skipped"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 409 {object} schema.ErrorResponse "The state of the server does not allow it (a feature that is off, or a conflict)"
// @Router /api/chats/files/undo [post]
func (h *Handlers) UndoFiles(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	var req schema.ChatsUndoRequest
	if err := c.Bind(&req); err != nil || req.Rev <= 0 {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "rev is required")
	}
	cs, conflicts, err := fc.f.Store().Undo(c.Request().Context(), fc.a, req.Rev, workspace.UndoOptions{SkipConflicts: req.SkipConflicts})
	out := schema.ChatsUndoResponse{Rev: cs.Rev}
	for _, k := range conflicts {
		out.Conflicts = append(out.Conflicts, schema.ChatsFileConflict{Path: fc.f.Rel(fc.p, k.Path), Reason: k.Reason})
	}
	if err != nil {
		if errors.Is(err, workspace.ErrConflict) {
			return c.JSON(http.StatusConflict, out)
		}
		return failFile(c, err)
	}
	return c.JSON(http.StatusOK, out)
}

// FileHistory lists change sets.
// @Summary List the changes to your files.
// @Tags chats
// @Param path query string false "folder or file inside your home; default /"
// @Param limit query int false "most entries to return (default 50, at most 200)"
// @Success 200 {object} schema.ChatsHistory "Change sets, newest first"
// @Router /api/chats/files/history [get]
func (h *Handlers) FileHistory(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	abs, err := fc.abs(c.QueryParam("path"), true)
	if err != nil {
		return failFile(c, err)
	}
	hist, err := fc.f.Store().History(c.Request().Context(), fc.a, workspace.HistoryQuery{Path: abs, Limit: intParam(c, "limit", 50, 200)})
	if err != nil {
		return failFile(c, err)
	}
	out := schema.ChatsHistory{Entries: make([]schema.ChatsHistoryEntry, 0, len(hist))}
	for _, cs := range hist {
		e := schema.ChatsHistoryEntry{Rev: cs.Rev, Title: cs.Title, Kind: string(cs.Kind), Chat: cs.ChatID, At: cs.CreatedAt}
		for _, op := range cs.Ops {
			o := schema.ChatsFileOp{Kind: string(op.Kind), Path: fc.f.Rel(fc.p, op.NewPath)}
			if op.Kind == workspace.OpDelete {
				o.Path = fc.f.Rel(fc.p, op.OldPath)
			}
			if op.Kind == workspace.OpMove {
				o.From = fc.f.Rel(fc.p, op.OldPath)
			}
			e.Ops = append(e.Ops, o)
		}
		out.Entries = append(out.Entries, e)
	}
	return c.JSON(http.StatusOK, out)
}

// SearchFiles searches your files.
// @Summary Search your files.
// @Description Finds files by name and lines inside text files that contain the query, ignoring case. Only what you can read is searched. Large and binary files are found by name only.
// @Tags chats
// @Param q query string true "text to find"
// @Param path query string false "search only below this folder; default /"
// @Param limit query int false "most hits to return (default 50, at most 200)"
// @Success 200 {object} schema.ChatsSearchResult "Name matches first, then lines"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/files/search [get]
func (h *Handlers) SearchFiles(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "q is required")
	}
	hits, err := fc.f.Search(c.Request().Context(), fc.p, q, c.QueryParam("path"), intParam(c, "limit", 50, 200))
	if err != nil {
		return failFile(c, err)
	}
	out := schema.ChatsSearchResult{Hits: make([]schema.ChatsSearchHit, 0, len(hits))}
	for _, hit := range hits {
		out.Hits = append(out.Hits, schema.ChatsSearchHit{Path: hit.Path, Kind: string(hit.Kind), Line: hit.Line, Snippet: hit.Snippet})
	}
	return c.JSON(http.StatusOK, out)
}
