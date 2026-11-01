package wellknown

// This file is the compatibility window for the settings files that sat at
// the top of the home before they moved into LocalAI/. It goes away with the
// next minor release: delete this file and the calls to AdoptTopLevelFiles
// and s.legacyRead.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// fixedNames are the settings files that have a fixed path.
var fixedNames = []Name{Persona, User, Shortcuts, Home, UI}

// legacyFile is where the settings file n was before LocalAI/.
func legacyFile(n Name) string { return "/" + string(n) + ".json" }

// bothLogged remembers which "both files exist" cases this process logged, so
// the log says it once per file version and not at every read. It is only a
// log filter: every frontend decides from the shared workspace.
var bothLogged sync.Map

// AdoptTopLevelFiles moves the settings files of a user that are still at the
// top of the home into LocalAI/. It acts as the user, in the user's home only,
// and is safe to call at any time, again, and from several frontends at once.
func (s *Service) AdoptTopLevelFiles(ctx context.Context, principal string) {
	if !s.Available() {
		return
	}
	for _, n := range fixedNames {
		s.adopt(ctx, principal, n)
	}
}

// adopt moves the old file of n into LocalAI/ when it should, and returns the
// workspace path to read instead of the new file when the move failed and only
// the old file exists, else "".
func (s *Service) adopt(ctx context.Context, principal string, n Name) string {
	oldAbs, err := s.f.Abs(principal, legacyFile(n))
	if err != nil {
		return ""
	}
	newAbs, err := s.f.Abs(principal, File(n))
	if err != nil {
		return ""
	}
	st, a := s.f.Store(), s.f.Actor(principal)
	old, err := st.Stat(ctx, a, oldAbs)
	if err != nil || old.Kind != workspace.KindFile {
		return ""
	}
	title := "Move " + string(n) + ".json into LocalAI/"
	cur, err := st.Stat(ctx, a, newAbs)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		err = s.move(ctx, a, oldAbs, newAbs, title)
		if err == nil || errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrExists) {
			// Moved, or another request moved it first.
			return ""
		}
		xlog.Warn("wellknown: could not move a settings file into LocalAI/; reading it where it is", "user", principal, "file", legacyFile(n), "error", err)
		return oldAbs
	case err != nil:
		return ""
	}
	// Both exist. The old file wins only when it was written after LocalAI
	// moved it: then it is a write that an old page or prompt sent to the old
	// place.
	moved := s.movedAt(ctx, principal, oldAbs, newAbs)
	if moved == 0 || old.Rev <= moved {
		if _, seen := bothLogged.LoadOrStore(fmt.Sprintf("%s\x00%s\x00%d", principal, n, old.Rev), true); !seen {
			xlog.Info("wellknown: a settings file is both at the top of the home and in LocalAI/; using LocalAI/", "user", principal, "file", legacyFile(n))
		}
		return ""
	}
	if err := s.pickUp(ctx, a, oldAbs, newAbs, cur, title); err != nil && !errors.Is(err, workspace.ErrNotFound) {
		xlog.Warn("wellknown: could not move a settings file into LocalAI/", "user", principal, "file", legacyFile(n), "error", err)
		return ""
	}
	xlog.Info("wellknown: moved a settings file written at the old place into LocalAI/", "user", principal, "file", legacyFile(n))
	return ""
}

func (s *Service) move(ctx context.Context, a workspace.Actor, from, to, title string) error {
	tx, err := s.f.Store().Begin(ctx, a, title)
	if err != nil {
		return err
	}
	if _, err := tx.Move(ctx, from, to); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Commit(); err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return err
	}
	return nil
}

// movedAt is the revision at which LocalAI moved from to to, or 0.
func (s *Service) movedAt(ctx context.Context, principal, from, to string) int64 {
	hist, err := s.f.Store().History(ctx, s.f.Actor(principal), workspace.HistoryQuery{Path: to, Limit: historyDepth})
	if err != nil {
		return 0
	}
	for _, cs := range hist {
		for _, op := range cs.Ops {
			if op.Kind == workspace.OpMove && op.OldPath == from && op.NewPath == to {
				return cs.Rev
			}
		}
	}
	return 0
}

// pickUp writes the old file's content as a new version of the new file and
// sends the old file to the Trash, in one change set.
func (s *Service) pickUp(ctx context.Context, a workspace.Actor, oldAbs, newAbs string, cur workspace.Node, title string) error {
	r, node, err := s.f.Store().Open(ctx, a, oldAbs)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	_ = r.Close()
	if err != nil {
		return err
	}
	if node.Size > MaxSize {
		// Too large to be a settings file: leave it for the user.
		return nil
	}
	tx, err := s.f.Store().Begin(ctx, a, title)
	if err != nil {
		return err
	}
	put, err := tx.Put(ctx, newAbs, bytes.NewReader(data), workspace.PutOptions{BaseVersion: cur.Version, MaxSize: MaxSize})
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if put.ConflictOf != "" {
		// Another request changed the new file first: try again at the next access.
		_ = tx.Rollback()
		return nil
	}
	if err := tx.Delete(ctx, oldAbs); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Commit(); err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return err
	}
	return nil
}

// legacyRead is the path to use for n: the new one, or the old one while the
// move failed (see adopt). home is the path inside the home.
func (s *Service) legacyRead(ctx context.Context, principal string, n Name, abs, home string) (string, string) {
	if File(n) == "" {
		return abs, home
	}
	if old := s.adopt(ctx, principal, n); old != "" {
		return old, strings.TrimSuffix(s.f.Rel(principal, old), "/")
	}
	return abs, home
}
