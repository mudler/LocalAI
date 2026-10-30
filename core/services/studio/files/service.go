// Package files is the workspace of the chats: each user's personal folder, the
// rules that keep users apart, and the file features built on it. A service can
// be disabled with a reason, and then every file feature says why it is off.
package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// AdminPrincipal is the principal the service uses to create homes and grant
// access. It cannot be a user: EnsureHome refuses it.
const AdminPrincipal = workspace.StudioAdmin

// EveryoneGroup is the group of every user. A grant to it gives a folder to all
// users, the ones that sign up later too. It cannot be a user: EnsureHome
// refuses it.
const EveryoneGroup = "@everyone"

// ErrUnavailable means the workspace is off. Its text is the reason.
var ErrUnavailable = errors.New("files are off")

// Service gives each user a personal folder in a workspace store.
type Service struct {
	ws     workspace.Store
	reason string

	mu    sync.Mutex
	homes map[string]bool // principals whose home is known to exist
	admin workspace.Actor

	sharedReady bool // the folders of shared and built-in skills exist

	blobs BlobOpener // nil: earlier versions cannot be read

	// sharedRoots lists the shared folders of a user, for the file tools'
	// /workspace/shared-<name> paths. Nil: the tools see no shared folder.
	sharedRoots SharedRootsFunc
}

// SharedRootsFunc lists the shared folders a user may use, with the mode of
// each.
type SharedRootsFunc func(ctx context.Context, a workspace.Actor) ([]session.SharedRoot, error)

// SetSharedRoots tells the file tools where the user's shared folders are. The
// shared folders are built on this service, so they are set after it. Call it
// once at start; nil removes them.
func (s *Service) SetSharedRoots(fn SharedRootsFunc) {
	s.mu.Lock()
	s.sharedRoots = fn
	s.mu.Unlock()
}

// quietLog drops what session.BuildView logs about a shared folder that is
// gone: the file tools answer "not found" for it.
var quietLog = slog.New(slog.DiscardHandler)

// New returns a Service on ws. The store must have been opened with
// AdminPrincipal as its admin.
func New(ws workspace.Store, opts ...Option) *Service {
	s := &Service{ws: ws, homes: map[string]bool{}, admin: workspace.Actor{Principal: AdminPrincipal}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Disabled returns a Service with no workspace. reason says why, in words for
// the user.
func Disabled(reason string) *Service {
	return &Service{reason: reason, homes: map[string]bool{}}
}

// Available reports whether the workspace works.
func (s *Service) Available() bool { return s != nil && s.ws != nil }

// Unavailable returns why the workspace is off, or "" when it works.
func (s *Service) Unavailable() string {
	if s.Available() {
		return ""
	}
	if s == nil || s.reason == "" {
		return "Files are not set up on this server."
	}
	return s.reason
}

// Store returns the workspace store, or nil when it is off.
func (s *Service) Store() workspace.Store {
	if !s.Available() {
		return nil
	}
	return s.ws
}

func validPrincipal(p string) bool {
	return p != "" && p != AdminPrincipal && p != EveryoneGroup && p != "." && p != ".." && len(p) <= 200 &&
		!strings.ContainsAny(p, "/\\\x00")
}

// EnsureHome creates the user's home folder and its Chats folder, and gives
// the user write access to the home and to nothing else. It is safe to call
// again and from several frontends at once.
func (s *Service) EnsureHome(ctx context.Context, principal string) error {
	if !s.Available() {
		return fmt.Errorf("%w: %s", ErrUnavailable, s.Unavailable())
	}
	if !validPrincipal(principal) {
		return fmt.Errorf("%w: the user name cannot name a folder", workspace.ErrBadName)
	}
	s.mu.Lock()
	known := s.homes[principal]
	s.mu.Unlock()
	if known {
		return nil
	}
	if err := s.ensureShared(ctx); err != nil {
		return err
	}
	if _, err := s.ws.Mkdir(ctx, s.admin, session.ChatsPath(principal)); err != nil && !errors.Is(err, workspace.ErrExists) {
		return fmt.Errorf("files: create the home of a user: %w", err)
	}
	if err := s.ws.Grant(ctx, s.admin, principal, session.HomePath(principal), workspace.ModeWrite); err != nil {
		return fmt.Errorf("files: give a user their home: %w", err)
	}
	// Everyone reads the shared and built-in skills; nobody but the server and
	// an administrator writes them.
	for _, p := range []string{SharedSkillsPath, BuiltinSkillsPath} {
		if err := s.ws.Grant(ctx, s.admin, principal, p, workspace.ModeRead); err != nil {
			return fmt.Errorf("files: let a user read %s: %w", p, err)
		}
	}
	s.mu.Lock()
	s.homes[principal] = true
	s.mu.Unlock()
	return nil
}
