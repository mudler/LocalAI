package files

import (
	"context"
	"errors"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ReadHome returns the text of a file in the home of principal. rel is a path
// inside the home. ok is false when the file does not exist. It reads as the
// user without a chat fence, for the host's own checks.
func (s *Service) ReadHome(ctx context.Context, principal, rel string, max int64) (text string, ok bool, err error) {
	if !s.Available() {
		return "", false, ErrUnavailable
	}
	abs, err := s.Abs(principal, rel)
	if err != nil {
		return "", false, err
	}
	text, _, err = s.readText(ctx, s.Actor(principal), abs, max)
	if errors.Is(err, workspace.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

// WriteHome replaces a file in the home of principal as one change set with
// the given title, and returns its revision, so the caller can undo it. It
// writes as the user: it is for a change the user confirmed. The content may
// be at most max bytes.
func (s *Service) WriteHome(ctx context.Context, principal, rel, content, title string, max int64) (int64, error) {
	if !s.Available() {
		return 0, ErrUnavailable
	}
	abs, err := s.Abs(principal, rel)
	if err != nil {
		return 0, err
	}
	if err := s.EnsureHome(ctx, principal); err != nil {
		return 0, err
	}
	title = strings.TrimSpace(title)
	a := s.Actor(principal)
	tx, err := s.ws.Begin(ctx, a, title)
	if err != nil {
		return 0, err
	}
	n, err := tx.Put(ctx, abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true, MaxSize: max, Title: title})
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	cs, err := tx.Commit()
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return 0, err
	}
	n.Rev = cs.Rev
	s.Written(ctx, n, principal)
	return cs.Rev, nil
}

// HomeFile is one file of a change that writes several.
type HomeFile struct {
	Path    string // inside the home
	Content string
}

// WriteHomeFiles replaces several files in the home of principal as one change
// set, so one undo restores all of them. It writes as the user, for a change
// the user confirmed. Each file may be at most max bytes. Nothing is written
// when one file fails.
func (s *Service) WriteHomeFiles(ctx context.Context, principal string, fs []HomeFile, title string, max int64) (int64, error) {
	if !s.Available() {
		return 0, ErrUnavailable
	}
	abs := make([]string, len(fs))
	for i, f := range fs {
		a, err := s.Abs(principal, f.Path)
		if err != nil {
			return 0, err
		}
		abs[i] = a
	}
	if err := s.EnsureHome(ctx, principal); err != nil {
		return 0, err
	}
	title = strings.TrimSpace(title)
	tx, err := s.ws.Begin(ctx, s.Actor(principal), title)
	if err != nil {
		return 0, err
	}
	nodes := make([]workspace.Node, len(fs))
	for i, f := range fs {
		n, err := tx.Put(ctx, abs[i], strings.NewReader(f.Content), workspace.PutOptions{Overwrite: true, MaxSize: max, Title: title})
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		nodes[i] = n
	}
	cs, err := tx.Commit()
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return 0, err
	}
	for _, n := range nodes {
		n.Rev = cs.Rev
		s.Written(ctx, n, principal)
	}
	return cs.Rev, nil
}

// DeleteHome moves a file or folder of the home of principal to the Trash, as
// the user, so it can be restored. A path that is not there is not an error.
func (s *Service) DeleteHome(ctx context.Context, principal, rel string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	abs, err := s.Abs(principal, rel)
	if err != nil {
		return err
	}
	if err := s.ws.Delete(ctx, s.Actor(principal), abs); err != nil && !errors.Is(err, workspace.ErrNotFound) {
		return err
	}
	return nil
}
