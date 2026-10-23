// SPDX-License-Identifier: MIT

// Command tables-faults is a TEST-ONLY build of the Tables executable. It is
// never packaged, published or installed outside the acceptance suites. It
// serves exactly what cmd/tables serves and adds two faults that a suite arms
// by creating a file in the "fault" directory of the add-on's data directory:
//
//   - crash-after-commit: the next row or schema change dies with the process
//     after its transaction committed and before the answer is sent. The file
//     is removed first, so the restarted process runs normally.
//   - hold-output: an output stream (an export) is not served while the file
//     exists, so a test can hold a transfer in flight and cancel it.
//
// Nothing here changes the product executable: it has no flag or environment
// variable for either fault.
package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/addons/tables/internal/server"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func armed(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, "fault", name))
	return err == nil
}

// held wraps a runtime so an armed hold-output fault delays the stream.
type held struct {
	contract.Runtime
	dir string
}

func (h held) OpenOutput(ctx context.Context, e contract.Envelope, handle string) (io.ReadCloser, error) {
	for armed(h.dir, "hold-output") {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return h.Runtime.OpenOutput(ctx, e, handle)
}

func main() {
	os.Exit(server.Run(os.Args, server.Hooks{
		Store: func(st *store.Store, dir string) {
			st.SetFailpoint(func(point string) error {
				if point == store.FailAfterCommit && os.Remove(filepath.Join(dir, "fault", "crash-after-commit")) == nil {
					os.Exit(1)
				}
				return nil
			})
		},
		Runtime: func(rt contract.Runtime, dir string) contract.Runtime { return held{Runtime: rt, dir: dir} },
	}))
}
