package studio

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/files/preview"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

const (
	// maxPreviewBytes is the largest file the server reads for a preview. A
	// larger one is a download only.
	maxPreviewBytes = 20 << 20
	// previewTimeout bounds the work on one file.
	previewTimeout = 10 * time.Second
	// previewSlots is how many previews are read at once. Each holds the file
	// in memory, so this bounds the memory as well as the CPU.
	previewSlots = 4
	// previewCacheSize is how many previews the cache keeps.
	previewCacheSize = 64
)

// previewCache keeps recent previews by the content they were read from (the
// blob's hash and the type), so a file read again after a check of the
// caller's access costs nothing, and a new version of a file is a new key.
// Every frontend of a cluster keeps its own: an entry never goes stale, so
// there is nothing to share (see docs/content/features/chats.md).
type previewCache struct {
	mu    sync.Mutex
	order *list.List
	items map[string]*list.Element
	slots chan struct{}
}

type previewEntry struct {
	key string
	val schema.ChatsFilePreview
}

func newPreviewCache() *previewCache {
	return &previewCache{order: list.New(), items: map[string]*list.Element{}, slots: make(chan struct{}, previewSlots)}
}

func (pc *previewCache) get(key string) (schema.ChatsFilePreview, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if el, ok := pc.items[key]; ok {
		pc.order.MoveToFront(el)
		return el.Value.(previewEntry).val, true
	}
	return schema.ChatsFilePreview{}, false
}

func (pc *previewCache) put(key string, val schema.ChatsFilePreview) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if el, ok := pc.items[key]; ok {
		pc.order.MoveToFront(el)
		return
	}
	pc.items[key] = pc.order.PushFront(previewEntry{key: key, val: val})
	for pc.order.Len() > previewCacheSize {
		last := pc.order.Back()
		pc.order.Remove(last)
		delete(pc.items, last.Value.(previewEntry).key)
	}
}

// PreviewFile reads what a spreadsheet, a CSV or TSV file, a Word document or
// a PowerPoint presentation holds, for the preview pane of a chat.
// @Summary Preview a spreadsheet, document or presentation.
// @Description Reads the values of a .xlsx, .xlsm, .csv or .tsv file (kind "table": every sheet, at most the first 200 rows and 30 columns, with the size of the whole sheet), the headings and paragraphs of a .docx (kind "document") or the slide titles, text and speaker notes of a .pptx (kind "slides"). The formatting is not kept. The same path rules as the download apply. Files over 20 MiB are not read.
// @Tags chats
// @Param path query string true "file inside your home"
// @Produce json
// @Success 200 {object} schema.ChatsFilePreview "The content of the file"
// @Failure 400 {object} schema.ErrorResponse "The path is not valid or is a folder"
// @Failure 404 {object} schema.ErrorResponse "No such file"
// @Failure 413 {object} schema.ErrorResponse "The file is over 20 MiB"
// @Failure 415 {object} schema.ErrorResponse "This type of file has no preview"
// @Failure 422 {object} schema.ErrorResponse "The file could not be read"
// @Router /api/chats/files/preview [get]
func (h *Handlers) PreviewFile(c echo.Context) error {
	fc, ok := h.files(c)
	if !ok {
		return nil
	}
	abs, err := fc.abs(c.QueryParam("path"), false)
	if err != nil {
		return failFile(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), previewTimeout)
	defer cancel()
	r, n, err := fc.f.Store().Open(ctx, fc.a, abs)
	if err != nil {
		return failFile(c, err)
	}
	defer func() { _ = r.Close() }()
	if n.Kind != workspace.KindFile {
		return failFile(c, workspace.ErrInvalid)
	}
	if !preview.Supported(n.Name) {
		return fail(c, http.StatusUnsupportedMediaType, "unsupported_preview", "this type of file has no preview; download it instead")
	}
	if n.Size > maxPreviewBytes {
		return fail(c, http.StatusRequestEntityTooLarge, "too_large", "the file is over 20 MiB; download it instead")
	}
	c.Response().Header().Set("Cache-Control", "private, no-store")
	key := n.Blob + "\x00" + strings.ToLower(path.Ext(n.Name))
	if n.Blob != "" {
		if out, ok := h.previews.get(key); ok {
			return c.JSON(http.StatusOK, out)
		}
	}
	select {
	case h.previews.slots <- struct{}{}:
		defer func() { <-h.previews.slots }()
	case <-ctx.Done():
		return previewFailed(c, ctx.Err())
	}
	data, err := io.ReadAll(io.LimitReader(r, maxPreviewBytes+1))
	if err != nil {
		return previewFailed(c, err)
	}
	if len(data) > maxPreviewBytes {
		return fail(c, http.StatusRequestEntityTooLarge, "too_large", "the file is over 20 MiB; download it instead")
	}
	out, err := preview.Build(ctx, n.Name, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return previewFailed(c, err)
	}
	if n.Blob != "" {
		h.previews.put(key, out)
	}
	return c.JSON(http.StatusOK, out)
}

func previewFailed(c echo.Context, err error) error {
	switch {
	case errors.Is(err, preview.ErrTooLarge):
		return fail(c, http.StatusRequestEntityTooLarge, "too_large", "the file unpacks to too much data to preview; download it instead")
	case errors.Is(err, preview.ErrInvalid):
		return fail(c, http.StatusUnprocessableEntity, "invalid_file", "the file could not be read; it may be damaged or not the type its name says")
	case errors.Is(err, context.DeadlineExceeded):
		return fail(c, http.StatusUnprocessableEntity, "too_complex", "the file took too long to read; download it instead")
	case errors.Is(err, context.Canceled):
		// The client went away; nobody reads the answer.
		return nil
	}
	xlog.Error("studio: file preview failed", "error", err)
	return fail(c, http.StatusInternalServerError, typeServerError, "the request failed")
}
