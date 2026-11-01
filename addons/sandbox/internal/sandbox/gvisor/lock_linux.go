//go:build linux

package gvisor

import (
	"context"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

// lockFile takes an exclusive lock on p, polling until ctx ends.
func lockFile(ctx context.Context, p string) (func(), error) { return ociimage.LockFile(ctx, p) }
