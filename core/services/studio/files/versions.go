package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ErrNoVersions means the service cannot read earlier versions of a file: it
// was built without the blob store.
var ErrNoVersions = errors.New("files: earlier versions are not available")

// BlobOpener reads a blob by its SHA-256. The blob store of the workspace is one.
type BlobOpener interface {
	Open(ctx context.Context, sha string) (io.ReadSeekCloser, error)
}

// Option configures a Service.
type Option func(*Service)

// WithBlobs lets the service read earlier versions of a file. The blobs are
// only reached through the history of a path that the user can read.
func WithBlobs(b BlobOpener) Option { return func(s *Service) { s.blobs = b } }

// FileVersion is one content a file had.
type FileVersion struct {
	Rev  int64
	At   time.Time
	Kind workspace.ChangeSetKind
	// ChatID is the chat that made the change, when a chat did.
	ChatID string
	Data   []byte
}

// maxMoveHops is how many moves Versions follows back.
const maxMoveHops = 4

// Versions calls fn with the contents the file abs had, newest first, as the
// history of the user shows them, until fn returns false. It looks at most at
// limit change sets per path. A version larger than maxSize is skipped without
// reading it. When the file was moved to abs, the versions it had at its old
// path follow, so a move (a rename, or LocalAI moving a settings file into its
// folder) keeps the file's past.
func (s *Service) Versions(ctx context.Context, principal, abs string, limit int, maxSize int64, fn func(FileVersion) bool) error {
	if !s.Available() {
		return fmt.Errorf("%w: %s", ErrUnavailable, s.Unavailable())
	}
	if s.blobs == nil {
		return ErrNoVersions
	}
	cur, before, skip := abs, int64(0), ""
	for hop := 0; hop <= maxMoveHops; hop++ {
		q := workspace.HistoryQuery{Path: cur, Limit: limit, BeforeRev: before}
		if hop > 0 {
			// The old path no longer exists: ask the history of the nearest
			// folder above it that does, and keep the rows of the old path.
			q.Path, q.Limit = s.liveAncestor(ctx, principal, cur), 1000
		}
		hist, err := s.ws.History(ctx, s.Actor(principal), q)
		if err != nil {
			if hop > 0 {
				return nil
			}
			return err
		}
		from, fromRev, fromBlob := "", int64(0), ""
		for _, cs := range hist {
			for _, op := range cs.Ops {
				if op.NewPath != cur || op.Kind == workspace.OpDelete {
					continue
				}
				if op.Kind == workspace.OpMove && op.OldPath != "" && op.OldPath != cur && from == "" {
					from, fromRev, fromBlob = op.OldPath, cs.Rev, op.NewBlob
				}
				if op.NewBlob == "" {
					continue
				}
				if skip != "" && op.NewBlob == skip {
					// The move showed this content already.
					skip = ""
					continue
				}
				skip = ""
				data, err := s.readBlob(ctx, op.NewBlob, maxSize)
				if errors.Is(err, workspace.ErrTooLarge) {
					continue
				}
				if err != nil {
					return err
				}
				if !fn(FileVersion{Rev: cs.Rev, At: cs.CreatedAt, Kind: cs.Kind, ChatID: cs.ChatID, Data: data}) {
					return nil
				}
			}
		}
		if from == "" {
			return nil
		}
		cur, before, skip = from, fromRev, fromBlob
	}
	return nil
}

// liveAncestor is the nearest folder above p that the user can still read,
// at most the home.
func (s *Service) liveAncestor(ctx context.Context, principal, p string) string {
	home := session.HomePath(principal)
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		if dir == home || !strings.HasPrefix(dir, home+"/") {
			return home
		}
		if n, err := s.ws.Stat(ctx, s.Actor(principal), dir); err == nil && n.Kind == workspace.KindDir {
			return dir
		}
	}
}

func (s *Service) readBlob(ctx context.Context, sha string, maxSize int64) ([]byte, error) {
	r, err := s.blobs.Open(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("files: read an earlier version: %w", err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("files: read an earlier version: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, workspace.ErrTooLarge
	}
	return data, nil
}
