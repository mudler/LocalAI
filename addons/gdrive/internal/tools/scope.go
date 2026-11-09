// SPDX-License-Identifier: MIT
package tools

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/addons/gdrive/internal/driveapi"
	"github.com/mudler/LocalAI/addons/internal/argutil"
)

// Media types the add-on treats on their own.
const (
	folderType = "application/vnd.google-apps.folder"
	nativeType = "application/vnd.google-apps."
)

// metaFields are the fields of a file the add-on reads. The size is a string
// in the API.
const metaFields = "id,name,mimeType,size,parents,trashed,modifiedTime,version,webViewLink"

// fileMeta is a file as the API describes it.
type fileMeta struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	Size         string   `json:"size"`
	Parents      []string `json:"parents"`
	Trashed      bool     `json:"trashed"`
	ModifiedTime string   `json:"modifiedTime"`
	Version      string   `json:"version"`
	WebViewLink  string   `json:"webViewLink"`
}

func (m *fileMeta) isFolder() bool { return m.MimeType == folderType }

// bytes is the stored size of a regular file, or -1 when the API gives none.
func (m *fileMeta) bytes() int64 {
	n, err := strconv.ParseInt(m.Size, 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// AllFolders is the resource key of a connection that allows every folder: the
// entry * of its list, which the host honors for this kind (wildcardResources).
// The agent names it as the folder argument.
const AllFolders = "*"

// all reports a call that the host allowed for every folder. The files it
// touches are not held to one folder: the person's login and the host's list
// are the limit.
func (c *call) all() bool { return c.key == AllFolders }

// checkFolder checks the folder argument: it has the shape of an ID, or is *
// for a connection that allows all folders, and is the key the host allowed
// for this call.
func (c *call) checkFolder(folder string) error {
	if !(folder == AllFolders && c.all()) {
		if err := checkID("folder", folder); err != nil {
			return err
		}
	}
	if c.key == "" || !strings.EqualFold(c.key, folder) {
		return bad("folder is not the one the host allowed")
	}
	return nil
}

// root returns the real ID of the allowed folder. The word root names the
// top of My Drive, which the service knows by another ID.
//
// With all folders there is no allowed folder: the top of My Drive stands in
// for it as the default place of a list or a new file.
func (c *call) root() (string, error) {
	if c.rootID != "" {
		return c.rootID, nil
	}
	if !strings.EqualFold(c.key, "root") && !c.all() {
		c.rootID = c.key
		return c.rootID, nil
	}
	m, err := c.fileMeta("root")
	if err != nil {
		return "", err
	}
	c.rootID = m.ID
	return c.rootID, nil
}

// fileMeta reads one file, from the cache when this call read it before. Each
// request counts against MaxLookups; a file the token cannot see is absent.
func (c *call) fileMeta(id string) (*fileMeta, error) {
	if m, ok := c.meta[id]; ok {
		return m, nil
	}
	if c.absent[id] {
		return nil, driveapi.ErrNotFound
	}
	if c.lookups >= MaxLookups {
		return nil, bad("the folder tree is too large to check where this file lies")
	}
	c.lookups++
	var m fileMeta
	q := url.Values{"fields": {metaFields}, "supportsAllDrives": {"true"}}
	if err := c.r.API.GetJSON(c.ctx, c.token, "/drive/v3/files/"+argutil.IDPath(id), q, &m); err != nil {
		if errors.Is(err, driveapi.ErrNotFound) {
			c.absent[id] = true
		}
		return nil, err
	}
	c.meta[id] = &m
	if id != m.ID && id == "root" {
		c.meta[m.ID] = &m
	}
	return &m, nil
}

// within reports whether the file or folder id lies in the allowed folder (or
// is it). A chain that ends elsewhere, that the token cannot see or that is
// deeper than maxDepth is outside: the check fails closed.
func (c *call) within(id string) (bool, error) {
	if c.all() {
		return true, nil
	}
	root, err := c.root()
	if err != nil {
		return false, err
	}
	return c.up(id, root, 0, map[string]bool{})
}

func (c *call) up(id, root string, depth int, seen map[string]bool) (bool, error) {
	if id == root {
		return true, nil
	}
	if depth >= maxDepth || seen[id] {
		return false, nil
	}
	seen[id] = true
	m, err := c.fileMeta(id)
	if err != nil {
		if errors.Is(err, driveapi.ErrNotFound) || errors.Is(err, driveapi.ErrForbidden) {
			return false, nil
		}
		return false, err
	}
	for _, p := range m.Parents {
		ok, err := c.up(p, root, depth+1, seen)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// inside returns the file when it lies in the allowed folder, and refuses
// with a fixed sentence when it does not. The sentence holds no name.
func (c *call) inside(id string) (*fileMeta, error) {
	m, err := c.fileMeta(id)
	if err != nil {
		return nil, err
	}
	if id == "root" {
		id = m.ID
	}
	ok, err := c.within(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, bad("the file is not in the folder the host allowed")
	}
	return m, nil
}

// item is a file in a result.
type item struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	NameCut  bool   `json:"nameCut,omitempty"`
	Kind     string `json:"kind"`
	MimeType string `json:"mimeType"`
	Bytes    *int64 `json:"bytes,omitempty"`
	Modified string `json:"modifiedTime,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Trashed  bool   `json:"trashed,omitempty"`
	Link     string `json:"link,omitempty"`
}

// link keeps a Google address of reasonable length and drops any other.
func link(u string) string {
	if (strings.HasPrefix(u, "https://drive.google.com/") || strings.HasPrefix(u, "https://docs.google.com/")) && len(u) <= 300 {
		return u
	}
	return ""
}

func (m *fileMeta) item() item {
	name, cutName := argutil.Runes(m.Name, MaxNameRunes)
	it := item{ID: m.ID, Name: name, NameCut: cutName, Kind: "file", MimeType: m.MimeType, Modified: m.ModifiedTime, Trashed: m.Trashed, Link: link(m.WebViewLink)}
	if m.isFolder() {
		it.Kind = "folder"
	}
	if n := m.bytes(); n >= 0 && !m.isFolder() {
		it.Bytes = &n
	}
	if len(m.Parents) > 0 {
		it.Parent = m.Parents[0]
	}
	return it
}
