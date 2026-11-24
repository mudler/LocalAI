// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
)

// FileView is what the administrator sees of a staged zip before the install
// dialog: what it is, how it relates to the installed add-on, and the digest
// the approval binds to. It carries no path and no credential.
type FileView struct {
	FileID string `json:"fileId"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	// SHA256 is the digest of the whole file, in hex. The install approval
	// binds to "sha256:" plus this value.
	SHA256   string `json:"sha256"`
	AddonID  string `json:"addonId"`
	Title    string `json:"title"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	// API is the host API range the manifest declares.
	API contract.VersionRange `json:"api"`
	// Required and Optional are the host operations the manifest asks for.
	Required         []string `json:"required"`
	Optional         []string `json:"optional"`
	Installed        bool     `json:"installed"`
	InstalledVersion string   `json:"installedVersion,omitempty"`
	// Relation is new, upgrade, same or downgrade.
	Relation string `json:"relation"`
	// Source and Origin say that nothing signs the file.
	Source string `json:"source"`
	Origin string `json:"origin"`
}

// FileBegin opens an upload. It returns the ID of the upload and the byte limit.
func (m *Manager) FileBegin(ctx context.Context, actor contract.Actor, name string) (string, int64, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return "", 0, e
	}
	store := m.uploadStore()
	if store == nil || m.installer == nil {
		return "", 0, ErrNoFileInstall
	}
	max, _ := m.installer.Limits()
	id, e := store.Begin(actor.UserID, packages.SafeFileName(name), max)
	return id, max, e
}

// FileChunk adds the next chunk to an upload.
func (m *Manager) FileChunk(ctx context.Context, actor contract.Actor, id string, offset int64, data []byte) error {
	if e := m.authorize(ctx, actor); e != nil {
		return e
	}
	store := m.uploadStore()
	if store == nil {
		return ErrNoFileInstall
	}
	return store.Append(id, actor.UserID, offset, data)
}

// FileAbort drops an upload that did not finish.
func (m *Manager) FileAbort(ctx context.Context, actor contract.Actor, id string) {
	if store := m.uploadStore(); store != nil && m.authorize(ctx, actor) == nil {
		store.Abort(id, actor.UserID)
	}
}

// FileFinish closes the upload and verifies the file as the install will: the
// staged package is built and thrown away. A file that fails is removed.
func (m *Manager) FileFinish(ctx context.Context, actor contract.Actor, id string) (FileView, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return FileView{}, e
	}
	store := m.uploadStore()
	if store == nil {
		return FileView{}, ErrNoFileInstall
	}
	entry, e := store.Finish(id, actor.UserID)
	if e != nil {
		return FileView{}, e
	}
	v, e := m.describeStaged(ctx, actor, entry)
	if e != nil {
		_ = store.Remove(entry.ID, actor.UserID)
		return FileView{}, e
	}
	return v, nil
}

// FileDescribe reads a staged file of the user again.
func (m *Manager) FileDescribe(ctx context.Context, actor contract.Actor, id string) (FileView, error) {
	if e := m.authorize(ctx, actor); e != nil {
		return FileView{}, e
	}
	store := m.uploadStore()
	if store == nil {
		return FileView{}, ErrNoFileInstall
	}
	_, entry, e := store.Open(id, actor.UserID)
	if e != nil {
		return FileView{}, e
	}
	return m.describeStaged(ctx, actor, entry)
}

// FileDrop removes a staged file of the user.
func (m *Manager) FileDrop(ctx context.Context, actor contract.Actor, id string) error {
	if e := m.authorize(ctx, actor); e != nil {
		return e
	}
	store := m.uploadStore()
	if store == nil {
		return ErrNoFileInstall
	}
	return store.Remove(id, actor.UserID)
}

func (m *Manager) describeStaged(ctx context.Context, actor contract.Actor, entry uploads.Entry) (FileView, error) {
	staged, e := m.stageUpload(ctx, actor, entry.ID)
	if e != nil {
		return FileView{}, e
	}
	rc := staged.Receipt()
	if e = m.installer.Discard(staged); e != nil {
		return FileView{}, e
	}
	m.mu.Lock()
	st := m.journal.Addons[rc.Manifest.ID]
	m.mu.Unlock()
	v := FileView{FileID: entry.ID, Name: entry.Name, Size: entry.Size, SHA256: entry.SHA256, AddonID: rc.Manifest.ID, Title: rc.Manifest.ID,
		Version: rc.Manifest.Version, Platform: rc.Manifest.OS + "/" + rc.Manifest.Arch, API: rc.Manifest.API,
		Source: zipformat.SourceID, Origin: zipformat.OriginLabel, Relation: FileNew}
	if u := rc.Manifest.Capabilities.UI; u != nil && u.Title != nil && u.Title.Default() != "" {
		v.Title = u.Title.Default()
	}
	v.Required, v.Optional = declared(rc)
	if v.Required == nil {
		v.Required = []string{}
	}
	if v.Optional == nil {
		v.Optional = []string{}
	}
	if cur, ok := currentReceipt(st); ok && st.Installed {
		v.Installed, v.InstalledVersion = true, cur.Manifest.Version
		v.Relation = FileRelation(true, cur.Manifest.Version, rc.Manifest.Version)
	}
	return v, nil
}

// IsFileRefusal reports whether e is a refusal of the content of a file.
func IsFileRefusal(e error) (*packages.FileError, bool) {
	var fe *packages.FileError
	return fe, errors.As(e, &fe)
}
