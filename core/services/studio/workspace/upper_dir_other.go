//go:build !linux

package workspace

import (
	"context"
	"errors"
)

// DirSource reads an overlay upper layer from a folder on this machine. It
// works on Linux only.
type DirSource struct {
	Root string
}

// Walk returns an error: overlay upper layers exist on Linux only.
func (d DirSource) Walk(ctx context.Context, fn func(UpperEntry) error) error {
	return errors.New("workspace: DirSource needs Linux")
}
