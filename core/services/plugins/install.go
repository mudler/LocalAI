package plugins

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

var (
	// ErrNeedsAdmin refuses a user the install of a plugin that runs code,
	// or any install while personal installs are off.
	ErrNeedsAdmin = errors.New("plugins: an administrator must install this plugin")
	// ErrNotHumanSurface refuses the install or the approval of code
	// through an API key or an MCP call: it needs the administrator's
	// browser session.
	ErrNotHumanSurface = errors.New("plugins: this needs the administrator's browser session")
	// ErrCodeForEveryone refuses an administrator's user-scope install of a
	// plugin that runs code: code is installed for everyone only.
	ErrCodeForEveryone = errors.New("plugins: a plugin that runs code is installed for everyone only")
	// ErrDigestMismatch means the files at the pinned commit are not the
	// files that were reviewed.
	ErrDigestMismatch = errors.New("plugins: the plugin changed since the review; inspect it again")
)

// Job is one install, update or rollback that runs in the operation queue.
type Job struct {
	ID       string
	PluginID string
	Update   bool
	// By is the user who asked for the change.
	By string
	// Run does the change. A cancelled context before Run writes nothing.
	Run func(ctx context.Context) error
}

// Queue runs jobs. The application wires it to galleryop.EnqueuePluginOp;
// a nil queue runs each job at once, in the caller.
type Queue interface {
	Enqueue(j Job)
}

type inlineQueue struct{}

func (inlineQueue) Enqueue(j Job) { _ = j.Run(context.Background()) }

func newJobID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// checkInstallRights applies spec 9.2 to an install of a plugin.
func (s *Service) checkInstallRights(a Actor, scope string, runsCode bool) error {
	if a.ID == "" {
		return ErrForbidden
	}
	switch scope {
	case store.ScopeInstance:
		if !a.Admin {
			return ErrForbidden
		}
		if runsCode && !a.HumanSurface {
			return ErrNotHumanSurface
		}
	case store.ScopeUser:
		if runsCode {
			if a.Admin {
				return ErrCodeForEveryone
			}
			return ErrNeedsAdmin
		}
		if !a.Admin && !s.userInstalls() {
			return ErrNeedsAdmin
		}
	default:
		return fmt.Errorf("%w: scope %q", ErrInvalid, scope)
	}
	return nil
}

// checkNewInstall refuses an install that cannot be created: a taken key,
// a user install of a plugin installed for everyone, or a limit.
func (s *Service) checkNewInstall(ctx context.Context, a Actor, pluginID, scope string) error {
	owner := ""
	if scope == store.ScopeUser {
		owner = a.ID
	}
	same, err := s.st.Installs(ctx, store.InstallFilter{PluginID: pluginID})
	if err != nil {
		return err
	}
	for _, in := range same {
		if in.Scope == scope && in.Owner == owner {
			return fmt.Errorf("%w: %s is installed already (install %s)", store.ErrConflict, pluginID, in.ID)
		}
		if scope == store.ScopeUser && in.Scope == store.ScopeInstance {
			return &store.InstanceInstalledError{InstallID: in.ID}
		}
	}
	f, limit := store.InstallFilter{Scope: store.ScopeInstance}, MaxInstanceInstalls
	if scope == store.ScopeUser {
		f, limit = store.InstallFilter{Scope: store.ScopeUser, Owner: a.ID}, MaxUserInstalls
	}
	n, err := s.st.Installs(ctx, f)
	if err != nil {
		return err
	}
	if len(n) >= limit {
		return fmt.Errorf("%w: at most %d %s installs", ErrLimit, limit, scope)
	}
	return nil
}

// checkNameFree refuses an install whose manifest name another plugin ID
// already uses where both would contribute: the skills and commands of a
// plugin are named <name>:<skill>, so fmt@a and fmt@b would give the same
// names. A user install meets the instance installs and the user's own; an
// instance install meets every install. The error names the first install.
func (s *Service) checkNameFree(ctx context.Context, a Actor, pluginID, scope, name string) error {
	if name == "" {
		return nil
	}
	f := store.InstallFilter{}
	if scope == store.ScopeUser {
		f.VisibleTo = a.ID
	}
	list, err := s.st.Installs(ctx, f)
	if err != nil {
		return err
	}
	for _, in := range list {
		if in.PluginID == pluginID {
			continue
		}
		pkg, err := s.st.Package(ctx, in.Digest)
		if err != nil {
			continue
		}
		if pkg.Name == name {
			return fmt.Errorf("%w: the plugin name %q is taken by %s (install %s); remove it first", store.ErrConflict, name, in.PluginID, in.ID)
		}
	}
	return nil
}

// Install confirms a preview and queues the install (spec 9.3 step 4). It
// checks the rights again, consumes the token and returns the job ID. The
// job fetches the plugin again at the same commit and refuses a digest that
// differs from the reviewed one.
//
// An instance install of a plugin ID that users installed for themselves
// replaces their user installs in the same transaction. Each of those users
// gets the instance install turned on, and must turn it on again when it
// runs code (their own installs could not). Their userConfig values are not
// carried over.
func (s *Service) Install(ctx context.Context, a Actor, secret, scope string) (string, error) {
	t, err := s.PreviewToken(ctx, a, secret)
	if err != nil {
		return "", err
	}
	if t.Kind != store.TokenInstall {
		return "", ErrTokenInvalid
	}
	if err := s.checkInstallRights(a, scope, t.RunsCode); err != nil {
		return "", err
	}
	if err := s.checkNewInstall(ctx, a, t.PluginID, scope); err != nil {
		return "", err
	}
	if err := s.checkNameFree(ctx, a, t.PluginID, scope, t.Name); err != nil {
		return "", err
	}
	if t, err = s.takeToken(ctx, a, secret, store.TokenInstall); err != nil {
		return "", err
	}
	id := newJobID()
	s.queue.Enqueue(Job{ID: id, PluginID: t.PluginID, By: a.ID, Run: func(ctx context.Context) error {
		return s.installJob(ctx, a, t, scope)
	}})
	return id, nil
}

func (s *Service) installJob(ctx context.Context, a Actor, t store.Token, scope string) error {
	release, err := s.lockPlugin(ctx, t.PluginID)
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	r, err := s.resolveEntry(ctx, t.PluginID)
	if err != nil {
		return err
	}
	c, err := s.fetchChecked(ctx, r, t.SHA)
	if err != nil {
		return err
	}
	defer c.done()
	if c.digest != t.Digest {
		return ErrDigestMismatch
	}
	runsCode := len(c.reasons) > 0
	if err := s.checkInstallRights(a, scope, runsCode); err != nil {
		return err
	}
	if err := s.checkNameFree(ctx, a, t.PluginID, scope, c.plugin.Name); err != nil {
		return err
	}
	if err := s.writePackage(ctx, c); err != nil {
		return err
	}
	pkg, err := s.packageOf(r, c)
	if err != nil {
		return err
	}
	now := s.now()
	in := store.Install{
		ID: newJobID(), PluginID: t.PluginID, Scope: scope, Digest: c.digest, SourceSHA: c.sha,
		RunsCode: runsCode, ApprovedBy: a.ID, ApprovedAt: now, ApprovedDigest: c.digest,
		State: store.StateInstalled, InstalledBy: a.ID, CreatedAt: now, UpdatedAt: now,
	}
	if scope == store.ScopeUser {
		in.Owner = a.ID
	}
	// A cancel before the transaction writes nothing; the files written
	// above are a package without a row, which the collection removes.
	if err := ctx.Err(); err != nil {
		return err
	}
	var replaced, replacedIDs []string
	if err := s.st.InstallTx(ctx, func(tx store.Tx) error {
		replaced, replacedIDs = nil, nil
		if err := tx.PutPackage(pkg); err != nil {
			return err
		}
		if scope == store.ScopeInstance {
			users, err := tx.Installs(store.InstallFilter{PluginID: t.PluginID, Scope: store.ScopeUser})
			if err != nil {
				return err
			}
			for _, u := range users {
				if err := tx.DeleteInstall(u.ID); err != nil {
					return err
				}
				replaced, replacedIDs = append(replaced, u.Owner), append(replacedIDs, u.ID)
			}
		}
		if err := tx.CreateInstall(in); err != nil {
			return err
		}
		if scope == store.ScopeUser {
			return tx.SaveUserState(store.UserState{Principal: a.ID, InstallID: in.ID, Enabled: true, UpdatedAt: now})
		}
		for _, owner := range replaced {
			if err := tx.SaveUserState(store.UserState{Principal: owner, InstallID: in.ID, Enabled: true, NeedsReenable: runsCode, UpdatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// The connectors follow the installs: a replaced personal install loses
	// its own, and each user the install is on for gets the new ones.
	for _, id := range replacedIDs {
		s.dropConnectors(ctx, id)
	}
	if scope == store.ScopeUser {
		s.syncConnectors(ctx, a.ID, in.ID, true)
	} else if !runsCode {
		for _, owner := range replaced {
			s.syncConnectors(ctx, owner, in.ID, true)
		}
	}
	return s.ensurePackageFiles(ctx, c)
}

// ensurePackageFiles writes the files again when a collection removed the
// folder between the write and the transaction: the folder had no row then.
func (s *Service) ensurePackageFiles(ctx context.Context, c *checked) error {
	if s.ws.Files == nil {
		return nil
	}
	ok, err := content.Exists(ctx, s.ws, s.root, c.digest)
	if err != nil || ok {
		return err
	}
	return s.writePackage(ctx, c)
}

func (s *Service) packageOf(r resolved, c *checked) (store.Package, error) {
	model, err := json.Marshal(c.model)
	if err != nil {
		return store.Package{}, err
	}
	skipped := make([]store.Skipped, 0, len(c.model.Skipped))
	for _, sk := range c.model.Skipped {
		skipped = append(skipped, store.Skipped{Component: sk.Component, Name: sk.Name, Reason: sk.ReasonCode})
	}
	return store.Package{
		Digest: c.digest, Name: c.plugin.Name, Version: c.plugin.Version, Description: c.plugin.Description,
		MarketplaceID: r.mkt.ID, Entry: r.entry.Name, SourceSHA: c.sha, SizeBytes: c.tree.Size,
		FileCount: len(c.tree.Files), Files: c.files(), Model: model, CodeReasons: c.reasons,
		CodeFiles: c.codeFiles, Skipped: skipped, CreatedAt: s.now(),
	}, nil
}
