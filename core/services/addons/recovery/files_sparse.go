//go:build !linux

package recovery

import (
	"io"
	"os"
)

// copyFileData copies everything: sparse ranges of virtual-machine images
// are a Linux concern here, and a plain copy already writes the apparent
// size.
func copyFileData(in, out *os.File, _ int64) error {
	_, err := io.Copy(out, in)
	return err
}
