// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

// ErrNoFileInstall: this owner has no staging area for local files.
var ErrNoFileInstall = errors.New("this add-on owner cannot install from a file")

// Relations between a staged file and what is installed.
const (
	FileNew       = "new"
	FileUpgrade   = "upgrade"
	FileSame      = "same"
	FileDowngrade = "downgrade"
)

type fileOwnerKey struct{}

// WithFileOwner names the user whose staged files a call may read. The host
// access service sets it from the authenticated actor; a call without it
// cannot stage a file.
func WithFileOwner(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, fileOwnerKey{}, userID)
}

func fileOwner(ctx context.Context) string {
	v, _ := ctx.Value(fileOwnerKey{}).(string)
	return v
}

// SetUploads sets the staging area of local files. The first call wins.
// It is read under the manager lock and outside it, so it has its own guard.
func (m *Manager) SetUploads(s *uploads.Store) {
	if s != nil {
		m.uploads.CompareAndSwap(nil, s)
	}
}

func (m *Manager) uploadStore() *uploads.Store { return m.uploads.Load() }

// stageUpload stages the package of a staged file of the user. It does not
// hold the manager lock: it reads and writes the disk only.
func (m *Manager) stageUpload(ctx context.Context, actor contract.Actor, fileID string) (packages.StagedPackage, error) {
	store := m.uploadStore()
	if store == nil || m.installer == nil {
		return packages.StagedPackage{}, ErrNoFileInstall
	}
	p, entry, err := store.Open(fileID, actor.UserID)
	if err != nil {
		return packages.StagedPackage{}, err
	}
	return m.installer.StageFile(ctx, p, entry.Name, nil)
}

func (m *Manager) dropUpload(actor contract.Actor, fileID string) {
	if store := m.uploadStore(); store != nil {
		if err := store.Remove(fileID, actor.UserID); err != nil && !errors.Is(err, uploads.ErrNotFound) {
			xlog.Warn("Could not remove a staged add-on file", "error", err)
		}
	}
}

// FileRelation says what installing the file would do to the add-on.
func FileRelation(installed bool, current, candidate string) string {
	if !installed {
		return FileNew
	}
	switch c := contract.CompareVersions(candidate, current); {
	case c > 0:
		return FileUpgrade
	case c < 0:
		return FileDowngrade
	}
	return FileSame
}

// fileVersionRefusal applies the version rules of an install from a file:
// the same version is never reinstalled, and an older one only after the
// administrator confirmed the downgrade. It returns nil when the file may go on.
func (m *Manager) fileVersionRefusal(cmd LifecycleCommand, next Status, staged packages.PackageReceipt) *FailureCause {
	cur, ok := currentReceipt(next)
	if !next.Installed || !ok {
		return nil
	}
	switch FileRelation(true, cur.Manifest.Version, staged.Manifest.Version) {
	case FileSame:
		return &FailureCause{Code: CauseVersionNotNewer}
	case FileDowngrade:
		if !cmd.Downgrade {
			return &FailureCause{Code: CauseDowngradeRefused}
		}
	}
	return nil
}

// previewFile stages the file of the user named by the context, reads its
// verified manifest and discards the stage. It changes no state of the add-ons.
func (m *Manager) previewFile(ctx context.Context, fileID string) (HostAccessRelease, error) {
	user := fileOwner(ctx)
	if user == "" {
		return HostAccessRelease{}, uploads.ErrNotFound
	}
	staged, e := m.stageUpload(ctx, contract.Actor{UserID: user}, fileID)
	if e != nil {
		return HostAccessRelease{}, e
	}
	rc := staged.Receipt()
	if e = m.installer.Discard(staged); e != nil {
		return HostAccessRelease{}, e
	}
	m.mu.Lock()
	installed := m.journal.Addons[rc.Manifest.ID].Installed
	m.mu.Unlock()
	out := HostAccessRelease{AddonID: rc.Manifest.ID, Title: rc.Manifest.ID, Version: rc.Manifest.Version, Digest: rc.Digest,
		Official: false, Installed: installed}
	if u := rc.Manifest.Capabilities.UI; u != nil && u.Title != nil && u.Title.Default() != "" {
		out.Title = u.Title.Default()
	}
	out.Required, out.Optional = declared(rc)
	return out, nil
}
