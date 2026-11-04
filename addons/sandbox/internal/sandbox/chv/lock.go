package chv

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

// lockFile takes an exclusive lock on p, polling until ctx ends. On a host
// without file locks (Windows) the backend cannot run, and New already
// refuses to start there.
func lockFile(ctx context.Context, p string) (func(), error) {
	unlock, err := ociimage.LockFile(ctx, p)
	if errors.Is(err, ociimage.ErrNoLock) {
		return nil, fmt.Errorf("%w: %v", sandbox.ErrUnsupported, err)
	}
	return unlock, err
}
