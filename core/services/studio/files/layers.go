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

	sharedGuest  = session.GuestRoot + "/shared-skills"
	builtinGuest = session.GuestRoot + "/shared-builtin-skills"
)

// SkillRoots returns the places where the user's skills are looked for, from
// the nearest layer to the farthest: any .skills folder of the user's home, the
// personal skills, the shared skills and the built-in ones. The Chats folder is
// not walked: it holds uploads, not skills.
func (s *Service) SkillRoots(principal string) []skills.Root {
	home := session.HomePath(principal)
	guestHome := session.GuestRoot + "/" + session.GuestHome
	return []skills.Root{
		{Layer: skills.LayerBuiltin, Path: BuiltinSkillsPath, Guest: builtinGuest},
		{Layer: skills.LayerShared, Path: SharedSkillsPath, Guest: sharedGuest},
		{Layer: skills.LayerPersonal, Path: home + "/" + skills.SkillsDir, Guest: guestHome + "/" + skills.SkillsDir},
		{Layer: skills.LayerFolder, Path: home, Guest: guestHome, Skip: []string{session.ChatsPath(principal)}},
	}
}

// SharedGuestPath maps a path the model uses for a shared layer to the
// workspace path, or "" when it is not one. A shared layer is read-only.
func SharedGuestPath(p string) string {
	for guest, real := range map[string]string{sharedGuest: SharedSkillsPath, builtinGuest: BuiltinSkillsPath} {
		if p == guest {
			return real
		}
		if len(p) > len(guest) && p[:len(guest)] == guest && p[len(guest)] == '/' {
			return real + p[len(guest):]
		}
	}
	return ""
}

// ensureShared creates the shared folders once.
func (s *Service) ensureShared(ctx context.Context) error {
	s.mu.Lock()
	ready := s.sharedReady
	s.mu.Unlock()
	if ready {
		return nil
	}
	for _, p := range []string{SharedSkillsPath, BuiltinSkillsPath} {
		if _, err := s.ws.Mkdir(ctx, s.admin, p); err != nil && !errors.Is(err, workspace.ErrExists) {
			return fmt.Errorf("files: create %s: %w", p, err)
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
	wanted := map[string]bool{BuiltinSkillsPath: true}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		target := path.Join(BuiltinSkillsPath, p)
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
			return fmt.Errorf("files: copy built-in skill file %s: %w", p, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.removeStaleBuiltin(ctx, wanted)
}

// removeStaleBuiltin deletes what the built-in layer holds beyond wanted.
func (s *Service) removeStaleBuiltin(ctx context.Context, wanted map[string]bool) error {
	var stale []string
	var walk func(dir string) error
	walk = func(dir string) error {
		children, err := s.ws.List(ctx, s.admin, dir)
		if err != nil {
			return fmt.Errorf("files: list built-in skills %s: %w", dir, err)
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
	if err := walk(BuiltinSkillsPath); err != nil {
		return err
	}
	if len(stale) == 0 {
		return nil
	}
	tx, err := s.ws.Begin(ctx, s.admin, "Remove built-in skills that left the release")
	if err != nil {
		return err
	}
	for _, p := range stale {
		if err := tx.Delete(ctx, p); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("files: remove built-in skill file %s: %w", p, err)
		}
	}
	_, err = tx.Commit()
	return err
}
