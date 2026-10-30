package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

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

// Versions calls fn with the contents the file abs had, newest first, as the
// history of the user shows them, until fn returns false. It looks at most at
// limit change sets. A version larger than maxSize is skipped without reading it.
func (s *Service) Versions(ctx context.Context, principal, abs string, limit int, maxSize int64, fn func(FileVersion) bool) error {
	if !s.Available() {
		return fmt.Errorf("%w: %s", ErrUnavailable, s.Unavailable())
	}
	if s.blobs == nil {
		return ErrNoVersions
	}
	hist, err := s.ws.History(ctx, s.Actor(principal), workspace.HistoryQuery{Path: abs, Limit: limit})
	if err != nil {
		return err
	}
	for _, cs := range hist {
		for _, op := range cs.Ops {
			if op.NewPath != abs || op.NewBlob == "" || op.Kind == workspace.OpDelete {
				continue
			}
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
	return nil
}

func (s *Service) readBlob(ctx context.Context, sha string, maxSize int64) ([]byte, error) {
	r, err := s.blobs.Open(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("files: read an earlier version: %w", err)
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("files: read an earlier version: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, workspace.ErrTooLarge
	}
	return data, nil
}
