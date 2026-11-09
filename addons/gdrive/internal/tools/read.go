// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/gdrive/internal/driveapi"
	"github.com/mudler/LocalAI/addons/internal/argutil"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type searchArgs struct {
	Folder         string `json:"folder"`
	Name           string `json:"name"`
	Text           string `json:"text"`
	MimeType       string `json:"mimeType"`
	ModifiedAfter  string `json:"modifiedAfter"`
	ModifiedBefore string `json:"modifiedBefore"`
	Parent         string `json:"parent"`
	IncludeTrashed bool   `json:"includeTrashed"`
	Cursor         string `json:"cursor"`
	PageSize       int    `json:"pageSize"`
}

type fileList struct {
	NextPageToken    string     `json:"nextPageToken"`
	IncompleteSearch bool       `json:"incompleteSearch"`
	Files            []fileMeta `json:"files"`
}

func pageSizeOr(n int) int {
	if n < 1 {
		return DefaultEntries
	}
	return min(n, MaxEntries)
}

// list asks the API for a page of files that match q.
func (c *call) list(q, cursor string, size int, order string) (fileList, error) {
	v := url.Values{
		"q":                         {q},
		"pageSize":                  {strconv.Itoa(size)},
		"fields":                    {"nextPageToken,incompleteSearch,files(" + metaFields + ")"},
		"supportsAllDrives":         {"true"},
		"includeItemsFromAllDrives": {"true"},
	}
	if c.all() {
		// The default corpus leaves out the shared drives the person has not
		// opened. All folders means every drive the login can see.
		v.Set("corpora", "allDrives")
	}
	if order != "" {
		v.Set("orderBy", order)
	}
	if cursor != "" {
		v.Set("pageToken", cursor)
	}
	var out fileList
	err := c.r.API.GetJSON(c.ctx, c.token, "/drive/v3/files", v, &out)
	return out, err
}

// folderOf checks that parent is a folder inside the allowed one.
func (c *call) folderOf(parent string) (*fileMeta, error) {
	if err := checkID("parent", parent); err != nil {
		return nil, err
	}
	m, err := c.inside(parent)
	if err != nil {
		return nil, err
	}
	if !m.isFolder() {
		return nil, bad("parent is not a folder")
	}
	return m, nil
}

func (c *call) search(raw map[string]json.RawMessage) (any, error) {
	var a searchArgs
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkFolder(a.Folder); err != nil {
		return nil, err
	}
	var cond conditions
	for _, f := range []struct{ what, v, format string }{
		{"name", a.Name, "name contains %s"},
		{"text", a.Text, "fullText contains %s"},
	} {
		if f.v == "" {
			continue
		}
		if err := argutil.CheckLine(f.what, f.v, 200, true); err != nil {
			return nil, err
		}
		cond.add(f.format, f.v)
	}
	if a.MimeType != "" {
		if !mimeShape.MatchString(a.MimeType) {
			return nil, bad("mimeType is not a media type")
		}
		cond.add("mimeType = %s", a.MimeType)
	}
	for _, t := range []struct{ what, v, format string }{
		{"modifiedAfter", a.ModifiedAfter, "modifiedTime > %s"},
		{"modifiedBefore", a.ModifiedBefore, "modifiedTime < %s"},
	} {
		if t.v == "" {
			continue
		}
		lit, ok := timeLiteral(t.v)
		if !ok {
			return nil, bad("%s is a date or a time like 2026-01-31", t.what)
		}
		cond.add(t.format, lit)
	}
	if err := argutil.CheckCursor(a.Cursor); err != nil {
		return nil, err
	}
	filters := len(cond)
	if a.Parent != "" {
		if _, err := c.folderOf(a.Parent); err != nil {
			return nil, err
		}
		cond.add("%s in parents", a.Parent)
		filters++
	}
	if filters == 0 {
		return nil, bad("give at least one search filter")
	}
	if !a.IncludeTrashed {
		cond = append(cond, "trashed = false")
	}
	page, err := c.list(cond.String(), a.Cursor, pageSizeOr(a.PageSize), "")
	if err != nil {
		return nil, err
	}
	items := []item{}
	for i := range page.Files {
		m := &page.Files[i]
		// Children of a parent that was checked are inside. Any other result
		// is kept only when the chain of its parents ends at the folder.
		if a.Parent == "" {
			ok, err := c.insideResult(m)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		items = append(items, m.item())
	}
	out := map[string]any{"items": items, "note": "Files outside the allowed folder are left out, so a page can hold fewer items than asked."}
	if page.NextPageToken != "" {
		out["nextCursor"] = page.NextPageToken
	}
	if page.IncompleteSearch {
		out["incomplete"] = true
	}
	return out, nil
}

// insideResult is within for a file that a list gave, whose parents are known.
func (c *call) insideResult(m *fileMeta) (bool, error) {
	if c.all() {
		return true, nil
	}
	root, err := c.root()
	if err != nil {
		return false, err
	}
	if m.ID == root {
		return true, nil
	}
	seen := map[string]bool{m.ID: true}
	for _, p := range m.Parents {
		ok, err := c.up(p, root, 1, seen)
		if err != nil {
			// The limit of lookups ends the check: what is not known to be
			// inside is left out.
			if errors.Is(err, driveapi.ErrUnauthorized) || isRate(err) {
				return false, err
			}
			if isBad(err) {
				return false, nil
			}
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (c *call) listFolder(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		Folder   string `json:"folder"`
		Parent   string `json:"parent"`
		Cursor   string `json:"cursor"`
		PageSize int    `json:"pageSize"`
	}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkFolder(a.Folder); err != nil {
		return nil, err
	}
	if err := argutil.CheckCursor(a.Cursor); err != nil {
		return nil, err
	}
	parent := a.Parent
	if parent == "" {
		root, err := c.root()
		if err != nil {
			return nil, err
		}
		parent = root
	}
	if _, err := c.folderOf(parent); err != nil {
		return nil, err
	}
	var cond conditions
	cond.add("%s in parents", parent)
	cond = append(cond, "trashed = false")
	page, err := c.list(cond.String(), a.Cursor, pageSizeOr(a.PageSize), "folder,name")
	if err != nil {
		return nil, err
	}
	items := make([]item, 0, len(page.Files))
	for i := range page.Files {
		items = append(items, page.Files[i].item())
	}
	out := map[string]any{"folderId": parent, "items": items}
	if page.NextPageToken != "" {
		out["nextCursor"] = page.NextPageToken
	}
	return out, nil
}

func (c *call) getMetadata(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		Folder string `json:"folder"`
		FileID string `json:"fileId"`
	}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkFolder(a.Folder); err != nil {
		return nil, err
	}
	if err := checkID("fileId", a.FileID); err != nil {
		return nil, err
	}
	m, err := c.inside(a.FileID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": m.item()}, nil
}

// exportFor says how a native file is turned into text, or reports that it
// cannot be. fallback is tried when the first format is refused.
func exportFor(mime string) (format, fallback, ext string, ok bool) {
	switch mime {
	case nativeType + "document":
		return "text/markdown", "text/plain", ".md", true
	case nativeType + "spreadsheet":
		return "text/csv", "", ".csv", true
	case nativeType + "presentation":
		return "text/plain", "", ".txt", true
	}
	return "", "", "", false
}

// textTypes are the media types besides text/* whose content is text.
var textTypes = map[string]bool{
	"application/json": true, "application/xml": true, "application/javascript": true, "application/x-javascript": true,
	"application/yaml": true, "application/x-yaml": true, "application/x-sh": true, "application/sql": true,
	"application/x-ndjson": true, "application/toml": true, "application/xhtml+xml": true, "application/rtf": false,
}

func isText(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	return strings.HasPrefix(mime, "text/") || textTypes[mime] || strings.HasSuffix(mime, "+json") || strings.HasSuffix(mime, "+xml")
}

type readArgs struct {
	Folder string `json:"folder"`
	FileID string `json:"fileId"`
	Offset int64  `json:"offset"`
}

func (c *call) readArgs(raw map[string]json.RawMessage) (readArgs, *fileMeta, error) {
	var a readArgs
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return a, nil, err
	}
	if err := c.checkFolder(a.Folder); err != nil {
		return a, nil, err
	}
	if err := checkID("fileId", a.FileID); err != nil {
		return a, nil, err
	}
	if a.Offset < 0 {
		return a, nil, bad("offset is 0 or more")
	}
	m, err := c.inside(a.FileID)
	return a, m, err
}

// source is where the text of a file comes from.
type source struct {
	// export is the format of an export; empty for a plain download.
	export string
	ext    string
	mime   string
}

func sourceOf(m *fileMeta) (source, bool) {
	if strings.HasPrefix(m.MimeType, nativeType) {
		f, _, ext, ok := exportFor(m.MimeType)
		return source{export: f, ext: ext, mime: f}, ok
	}
	return source{mime: m.MimeType}, isText(m.MimeType)
}

// fetch returns the bytes [offset, offset+max) of the text of the file, and
// its whole length.
func (c *call) fetch(m *fileMeta, s source, offset int64, max int) ([]byte, int64, error) {
	if s.export == "" {
		return c.download(m, offset, max)
	}
	body, err := c.export(m, s.export)
	if err != nil && s.export == "text/markdown" && errors.Is(err, driveapi.ErrInvalid) {
		// An older service does not export a document as markdown.
		body, err = c.export(m, "text/plain")
	}
	if err != nil {
		return nil, 0, err
	}
	total := int64(len(body))
	if offset >= total {
		return nil, total, nil
	}
	end := min(total, offset+int64(max))
	return body[offset:end], total, nil
}

func (c *call) export(m *fileMeta, format string) ([]byte, error) {
	q := url.Values{"mimeType": {format}, "supportsAllDrives": {"true"}}
	r, err := c.r.API.Do(c.ctx, c.token, driveapi.Request{Path: "/drive/v3/files/" + argutil.IDPath(m.ID) + "/export", Query: q}, driveapi.MaxBody)
	return r.Body, err
}

func (c *call) download(m *fileMeta, offset int64, max int) ([]byte, int64, error) {
	size := m.bytes()
	if size == 0 {
		return nil, 0, nil
	}
	if size > 0 && offset >= size {
		return nil, size, nil
	}
	q := url.Values{"alt": {"media"}, "supportsAllDrives": {"true"}}
	h := http.Header{"Range": {"bytes=" + strconv.FormatInt(offset, 10) + "-" + strconv.FormatInt(offset+int64(max)-1, 10)}}
	r, err := c.r.API.Do(c.ctx, c.token, driveapi.Request{Path: "/drive/v3/files/" + argutil.IDPath(m.ID), Query: q, Header: h}, driveapi.MaxBody)
	if err != nil {
		if errors.Is(err, driveapi.ErrRange) {
			return nil, size, nil
		}
		return nil, 0, err
	}
	if r.Status == http.StatusPartialContent {
		total := size
		if _, after, ok := strings.Cut(r.Header.Get("Content-Range"), "/"); ok {
			if n, err := strconv.ParseInt(after, 10, 64); err == nil {
				total = n
			}
		}
		return r.Body, total, nil
	}
	// The service ignored the range and sent the whole file.
	total := int64(len(r.Body))
	if offset >= total {
		return nil, total, nil
	}
	return r.Body[offset:min(total, offset+int64(max))], total, nil
}

func (c *call) readFile(raw map[string]json.RawMessage) (any, error) {
	a, m, err := c.readArgs(raw)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"file": m.item()}
	if m.isFolder() {
		out["content"] = nil
		out["note"] = "This is a folder. Use list-folder to see what is in it."
		return out, nil
	}
	s, ok := sourceOf(m)
	if !ok {
		out["content"] = nil
		out["note"] = "This file has no text form that this tool can read. Only the details are given."
		return out, nil
	}
	data, total, err := c.fetch(m, s, a.Offset, MaxFileBytes)
	if err != nil {
		return nil, err
	}
	if a.Offset > 0 && a.Offset >= total && total > 0 {
		return nil, bad("offset is past the end of the file at %d bytes", total)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		out["content"] = nil
		out["note"] = "This file is not text. Only the details are given."
		return out, nil
	}
	// The window starts inside a character when the offset does: move on to
	// the next one.
	start := a.Offset
	for len(data) > 0 && !utf8.RuneStart(data[0]) && start-a.Offset < 3 {
		data, start = data[1:], start+1
	}
	if len(data) >= MaxFileBytes {
		data = trimPartial(data)
	}
	// Shrink the window until the whole result fits what the host takes.
	limit := len(data)
	for {
		window, _ := argutil.Cut(string(data), limit)
		// Offsets count the bytes of the file, so they come from the window
		// as it is; the text the model reads may differ if the file is not UTF-8.
		next := start + int64(len(window))
		text := strings.ToValidUTF8(window, "\ufffd")
		res := map[string]any{"file": m.item(), "content": text, "offset": start, "bytes": len(window), "totalBytes": total}
		if s.export != "" {
			res["exportedAs"] = s.export
		}
		if next < total {
			res["truncated"] = true
			res["nextOffset"] = next
		}
		payload, err := c.r.Encode(res)
		if err != nil {
			return nil, err
		}
		if len(payload) <= MaxPayload-(4<<10) || limit <= 1<<10 {
			if next < total {
				res["note"] = "Only part of the text is shown. Read the rest with offset set to nextOffset."
			}
			return res, nil
		}
		limit = limit * 3 / 4
	}
}

func isRate(err error) bool {
	var l *contract.ErrRateLimited
	var s *driveapi.ServerError
	return errors.As(err, &l) || errors.As(err, &s)
}

func isBad(err error) bool { return errors.Is(err, protocol.ErrBadRequest) }

// trimPartial drops a character that the end of a window cut in two.
func trimPartial(b []byte) []byte {
	for i := 1; i <= 3 && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}
