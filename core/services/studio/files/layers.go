package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The two folders of skills that every user may read and none may write.
const (
	// SharedSkillsPath holds the skills an administrator gives to everyone.
	SharedSkillsPath = "/skills"
	// BuiltinSkillsPath holds the skills that ship with the server. The server
	// writes it at startup.
	BuiltinSkillsPath = "/builtin-skills"
	// AddonSkillsPath holds the skills that installed add-ons ship, one folder
	// per add-on: /addon-skills/<add-on>/<skill>/SKILL.md. The add-on owner
	// writes it when it installs, upgrades or starts.
	AddonSkillsPath = "/addon-skills"
)

// The paths the model uses for the layers: their mount names in a chat's view
// (session.SkillLayers), where the commands see them too.
var (
	sharedGuest  = session.GuestRoot + "/" + session.SkillLayers[0].At
	builtinGuest = session.GuestRoot + "/" + session.SkillLayers[1].At
	addonGuest   = session.GuestRoot + "/" + session.SkillLayers[2].At
)

// readOnlyLayers are the skill layers every user reads and none writes, by
// workspace path, with the path the model sees for each.
var readOnlyLayers = []struct{ real, guest string }{
	{SharedSkillsPath, sharedGuest},
	{BuiltinSkillsPath, builtinGuest},
	{AddonSkillsPath, addonGuest},
}

// AddonSkillsRoot returns the root of the add-on layer for one add-on, for
// skills.Resolve. title is the add-on's title, shown as the provider.
func AddonSkillsRoot(addonID, title string) skills.Root {
	return skills.Root{Layer: skills.LayerAddon, Addon: addonID, AddonTitle: title,
		Path: AddonSkillsPath + "/" + addonID, Guest: addonGuest + "/" + addonID}
}

// SkillRoots returns the places where the user's skills are looked for, from
// the nearest layer to the farthest: any .skills folder of the user's home, the
// personal skills, the shared skills and the built-in ones. The Chats folder is
// not walked: it holds uploads, not skills.
//
// addons are the roots of the add-on layer the chat may use now (see
// AddonSkillsRoot); the caller decides which add-ons are usable.
func (s *Service) SkillRoots(principal string, addons ...skills.Root) []skills.Root {
	home := session.HomePath(principal)
	guestHome := session.GuestRoot + "/" + session.GuestHome
	roots := []skills.Root{
		{Layer: skills.LayerBuiltin, Path: BuiltinSkillsPath, Guest: builtinGuest},
	}
	for _, r := range addons {
		if r.Layer == skills.LayerAddon {
			roots = append(roots, r)
		}
	}
	return append(roots,
		skills.Root{Layer: skills.LayerShared, Path: SharedSkillsPath, Guest: sharedGuest},
		skills.Root{Layer: skills.LayerPersonal, Path: home + "/" + skills.SkillsDir, Guest: guestHome + "/" + skills.SkillsDir},
		skills.Root{Layer: skills.LayerFolder, Path: home, Guest: guestHome, Skip: []string{session.ChatsPath(principal)}},
	)
}

// SharedGuestPath maps a path the model uses for a shared layer to the
// workspace path, or "" when it is not one. A shared layer is read-only.
func SharedGuestPath(p string) string {
	real, _, _ := sharedLayer(p)
	return real
}

// sharedLayer maps a guest path of a shared layer to its workspace path, and
// returns the layer's workspace folder and guest folder too.
func sharedLayer(p string) (real, layer, guest string) {
	for _, l := range readOnlyLayers {
		if p == l.guest {
			return l.real, l.real, l.guest
		}
		if len(p) > len(l.guest) && p[:len(l.guest)] == l.guest && p[len(l.guest)] == '/' {
			return l.real + p[len(l.guest):], l.real, l.guest
		}
	}
	return "", "", ""
}

// ensureShared creates the shared folders once.
func (s *Service) ensureShared(ctx context.Context) error {
	s.mu.Lock()
	ready := s.sharedReady
	s.mu.Unlock()
	if ready {
		return nil
	}
	for _, l := range readOnlyLayers {
		if _, err := s.ws.Mkdir(ctx, s.admin, l.real); err != nil && !errors.Is(err, workspace.ErrExists) {
			return fmt.Errorf("files: create %s: %w", l.real, err)
		}
	}
	s.mu.Lock()
	s.sharedReady = true
	s.mu.Unlock()
	return nil
}

// EnsureShared creates the folders of shared and built-in skills.
func (s *Service) EnsureShared(ctx context.Context) error {
	if !s.Available() {
		return fmt.Errorf("%w: %s", ErrUnavailable, s.Unavailable())
	}
	return s.ensureShared(ctx)
}

// SyncBuiltin makes the built-in layer hold the tree of skills. A file that
// already holds the same bytes is left alone, so running it at every start
// costs nothing and changes nothing. A file or folder of the layer that is not
// in the tree any more (a skill removed in a later release) is deleted, in one
// change set.
func (s *Service) SyncBuiltin(ctx context.Context, fsys fs.FS) error {
	if err := s.EnsureShared(ctx); err != nil {
		return err
	}
	return s.syncTree(ctx, BuiltinSkillsPath, fsys, "built-in skill", "Remove built-in skills that left the release")
}

// SyncAddonSkills makes the add-on layer of one add-on hold its skills: fsys
// holds one folder per skill, as the package ships them under skills/. It
// works like SyncBuiltin and never touches the folder of another add-on. An
// empty tree leaves an empty folder; RemoveAddonSkills deletes it.
func (s *Service) SyncAddonSkills(ctx context.Context, addonID string, fsys fs.FS) error {
	if !skills.ValidName(addonID) {
		return fmt.Errorf("%w: %q is not an add-on ID", workspace.ErrBadName, addonID)
	}
	if err := s.EnsureShared(ctx); err != nil {
		return err
	}
	base := AddonSkillsPath + "/" + addonID
	if _, err := s.ws.Mkdir(ctx, s.admin, base); err != nil && !errors.Is(err, workspace.ErrExists) {
		return fmt.Errorf("files: create %s: %w", base, err)
	}
	return s.syncTree(ctx, base, fsys, "add-on skill", "Remove add-on skills that left the package")
}

// RemoveAddonSkills deletes the skills of an add-on, for an uninstall. An
// add-on with no skills is not an error.
func (s *Service) RemoveAddonSkills(ctx context.Context, addonID string) error {
	if !skills.ValidName(addonID) {
		return fmt.Errorf("%w: %q is not an add-on ID", workspace.ErrBadName, addonID)
	}
	if err := s.EnsureShared(ctx); err != nil {
		return err
	}
	base := AddonSkillsPath + "/" + addonID
	if _, err := s.ws.Stat(ctx, s.admin, base); errors.Is(err, workspace.ErrNotFound) {
		return nil // nothing to remove, and no empty change set in the history
	} else if err != nil {
		return err
	}
	return s.deleteAll(ctx, []string{base}, "Remove the skills of an uninstalled add-on")
}

// AddonSkillIDs lists the add-ons that have a folder in the add-on layer.
func (s *Service) AddonSkillIDs(ctx context.Context) ([]string, error) {
	if err := s.EnsureShared(ctx); err != nil {
		return nil, err
	}
	children, err := s.ws.List(ctx, s.admin, AddonSkillsPath)
	if err != nil {
		return nil, fmt.Errorf("files: list add-on skills: %w", err)
	}
	var out []string
	for _, c := range children {
		if c.Kind == workspace.KindDir {
			out = append(out, c.Name)
		}
	}
	return out, nil
}

// syncTree makes the folder base hold the tree fsys: it writes every file and
// deletes, in one change set, what base holds beyond the tree. what names a
// file in an error, message is the text of the change set.
func (s *Service) syncTree(ctx context.Context, base string, fsys fs.FS, what, message string) error {
	wanted := map[string]bool{base: true}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		target := path.Join(base, p)
		wanted[target] = true
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		_, err = s.ws.Put(ctx, s.admin, target, bytesReader(data), workspace.PutOptions{Overwrite: true})
		if err != nil {
			return fmt.Errorf("files: copy %s file %s: %w", what, p, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.removeStale(ctx, base, wanted, message)
}

// removeStale deletes what base holds beyond wanted.
func (s *Service) removeStale(ctx context.Context, base string, wanted map[string]bool, message string) error {
	var stale []string
	var walk func(dir string) error
	walk = func(dir string) error {
		children, err := s.ws.List(ctx, s.admin, dir)
		if err != nil {
			return fmt.Errorf("files: list skills %s: %w", dir, err)
		}
		for _, c := range children {
			switch {
			case !wanted[c.Path]:
				stale = append(stale, c.Path) // a folder goes with everything below it
			case c.Kind == workspace.KindDir:
				if err := walk(c.Path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(base); err != nil {
		return err
	}
	return s.deleteAll(ctx, stale, message)
}

// deleteAll deletes the paths in one change set. A path that is gone already
// is not an error.
func (s *Service) deleteAll(ctx context.Context, paths []string, message string) error {
	if len(paths) == 0 {
		return nil
	}
	tx, err := s.ws.Begin(ctx, s.admin, message)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if err := tx.Delete(ctx, p); err != nil {
			if errors.Is(err, workspace.ErrNotFound) {
				continue
			}
			_ = tx.Rollback()
			return fmt.Errorf("files: remove skill file %s: %w", p, err)
		}
	}
	_, err = tx.Commit()
	return err
}
