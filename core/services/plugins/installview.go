package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// InstallView is an install as the API lists it, with the caller's own
// state.
type InstallView struct {
	ID             string    `json:"id"`
	PluginID       string    `json:"plugin"`
	Name           string    `json:"name"`
	Version        string    `json:"version,omitempty"`
	Description    string    `json:"description,omitempty"`
	Scope          string    `json:"scope"`
	Owner          string    `json:"owner,omitempty"`
	Digest         string    `json:"digest"`
	SourceSHA      string    `json:"sha"`
	PreviousDigest string    `json:"previousDigest,omitempty"`
	RunsCode       bool      `json:"runsCode"`
	State          string    `json:"state"`
	UpdateSHA      string    `json:"updateSha,omitempty"`
	UpdateDigest   string    `json:"updateDigest,omitempty"`
	ApprovedBy     string    `json:"approvedBy,omitempty"`
	ApprovedAt     time.Time `json:"approvedAt"`
	InstalledBy    string    `json:"installedBy,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	// Enabled and NeedsReenable are the caller's own state.
	Enabled       bool `json:"enabled"`
	NeedsReenable bool `json:"needsReenable"`
}

// InstallDetail is one install with what its package contributes.
type InstallDetail struct {
	InstallView
	Model       Model           `json:"model"`
	Files       []store.File    `json:"files"`
	SizeBytes   int64           `json:"sizeBytes"`
	CodeReasons []string        `json:"codeReasons"`
	CodeFiles   []string        `json:"codeFiles"`
	Skipped     []store.Skipped `json:"skipped"`
	Hosts       []string        `json:"hosts,omitempty"`
}

// viewableInstall returns an install the actor can read: what
// visibleInstall gives, and for an administrator every install. A user
// install of another user is not found for a user, as if it did not exist.
func (s *Service) viewableInstall(ctx context.Context, a Actor, id string) (store.Install, error) {
	if a.ID == "" {
		return store.Install{}, ErrForbidden
	}
	in, err := s.getInstall(ctx, id)
	if err != nil {
		return store.Install{}, err
	}
	if !visibleTo(a, in) {
		return store.Install{}, fmt.Errorf("%w: install %q", ErrNotFound, id)
	}
	return in, nil
}

// visibleInstall returns the install when the actor can see it as a user: an
// install for everyone, or the actor's own user install. Another user's
// install is ErrNotFound, as if it did not exist, also for an administrator:
// the caller reads or writes the actor's own state of the install.
func (s *Service) visibleInstall(ctx context.Context, a Actor, installID string) (store.Install, error) {
	return s.viewableInstall(ctx, Actor{ID: a.ID}, installID)
}

// ListInstalls lists the instance installs and the caller's own. With all,
// an administrator lists every install; a user gets ErrForbidden.
func (s *Service) ListInstalls(ctx context.Context, a Actor, all bool) ([]InstallView, error) {
	if a.ID == "" || (all && !a.Admin) {
		return nil, ErrForbidden
	}
	f := store.InstallFilter{VisibleTo: a.ID}
	if all {
		f = store.InstallFilter{}
	}
	list, err := s.st.Installs(ctx, f)
	if err != nil {
		return nil, err
	}
	states, err := s.st.UserStates(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	mine := make(map[string]store.UserState, len(states))
	for _, us := range states {
		mine[us.InstallID] = us
	}
	out := make([]InstallView, 0, len(list))
	for _, in := range list {
		v := installView(in, mine[in.ID])
		if pkg, err := s.st.Package(ctx, in.Digest); err == nil {
			v.Name, v.Version, v.Description = pkg.Name, pkg.Version, pkg.Description
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func installView(in store.Install, us store.UserState) InstallView {
	return InstallView{
		ID: in.ID, PluginID: in.PluginID, Scope: in.Scope, Owner: in.Owner, Digest: in.Digest,
		SourceSHA: in.SourceSHA, PreviousDigest: in.PreviousDigest, RunsCode: in.RunsCode, State: in.State,
		UpdateSHA: in.UpdateSHA, UpdateDigest: in.UpdateDigest, ApprovedBy: in.ApprovedBy, ApprovedAt: in.ApprovedAt,
		InstalledBy: in.InstalledBy, CreatedAt: in.CreatedAt, UpdatedAt: in.UpdatedAt,
		Enabled: us.Enabled, NeedsReenable: us.NeedsReenable,
	}
}

// GetInstall returns one install the caller can see, with its package.
func (s *Service) GetInstall(ctx context.Context, a Actor, id string) (InstallDetail, error) {
	in, err := s.viewableInstall(ctx, a, id)
	if err != nil {
		return InstallDetail{}, err
	}
	us, err := s.st.UserState(ctx, a.ID, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return InstallDetail{}, err
	}
	pkg, err := s.st.Package(ctx, in.Digest)
	if err != nil {
		return InstallDetail{}, fmt.Errorf("plugins: package of install %s: %w", id, err)
	}
	var m Model
	if err := json.Unmarshal(pkg.Model, &m); err != nil {
		return InstallDetail{}, fmt.Errorf("plugins: model of %s: %w", in.Digest, err)
	}
	d := InstallDetail{
		InstallView: installView(in, us), Model: m, Files: pkg.Files, SizeBytes: pkg.SizeBytes,
		CodeReasons: pkg.CodeReasons, CodeFiles: pkg.CodeFiles, Skipped: pkg.Skipped, Hosts: m.Hosts(),
	}
	d.Name, d.Version, d.Description = pkg.Name, pkg.Version, pkg.Description
	return d, nil
}

// openPackageFile opens one file of a stored package.
func (s *Service) openPackageFile(ctx context.Context, digest, path string) (io.ReadSeekCloser, error) {
	if s.ws.Files == nil {
		return nil, fmt.Errorf("%w: the package files are not kept on this server", ErrNotFound)
	}
	if err := content.CheckRelPath(path); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	r, err := content.Open(ctx, s.ws, s.root, digest, path)
	if err != nil {
		return nil, fmt.Errorf("%w: file %q", ErrNotFound, path)
	}
	return r, nil
}

// InstallFile opens one file of the current package of an install the
// caller can see.
func (s *Service) InstallFile(ctx context.Context, a Actor, id, path string) (io.ReadSeekCloser, error) {
	in, err := s.viewableInstall(ctx, a, id)
	if err != nil {
		return nil, err
	}
	return s.openPackageFile(ctx, in.Digest, path)
}

// PreviewFile opens one file of the package a preview token names (an
// inspect or an update preview), for the review. It does not use the token
// up.
func (s *Service) PreviewFile(ctx context.Context, a Actor, secret, path string) (io.ReadSeekCloser, error) {
	t, err := s.PreviewToken(ctx, a, secret)
	if err != nil {
		return nil, err
	}
	return s.openPackageFile(ctx, t.Digest, path)
}
