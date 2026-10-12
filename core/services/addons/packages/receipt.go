// SPDX-License-Identifier: MIT
package packages

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type PackageReceipt struct {
	Digest       string            `json:"digest"`
	SourceID     string            `json:"sourceId"`
	SourceURL    string            `json:"sourceUrl"`
	Channel      string            `json:"channel,omitempty"`
	Selection    string            `json:"selection"`
	Reference    string            `json:"reference"`
	Verification string            `json:"verification"`
	Manifest     contract.Manifest `json:"manifest"`
	Directory    string            `json:"directory"`
}

// StagedPackage is an opaque, single-use capability bound to the creating installer.
type StagedPackage struct{ state *stagedState }
type stagedState struct {
	mu      sync.Mutex
	owner   *Installer
	dir     string
	receipt PackageReceipt
	used    bool
}

func (i *Installer) Commit(ctx context.Context, s StagedPackage) (PackageReceipt, error) {
	if s.state == nil {
		return PackageReceipt{}, ErrPackage
	}
	st := s.state
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.owner != i || st.used {
		return PackageReceipt{}, ErrPackage
	}
	if e := ctx.Err(); e != nil {
		return PackageReceipt{}, e
	}
	// The random basename is reserved by Stage in this private root; renaming never
	// targets an existing version or deletes a previously published package.
	dest := filepath.Join(filepath.Dir(st.dir), "package-"+filepath.Base(st.dir))
	if _, e := os.Lstat(dest); !os.IsNotExist(e) {
		return PackageReceipt{}, ErrPackage
	}
	receipt := st.receipt
	receipt.Directory = dest
	b, e := json.Marshal(receipt)
	if e != nil {
		return PackageReceipt{}, e
	}
	if e = os.WriteFile(filepath.Join(st.dir, "receipt.json"), b, 0400); e != nil {
		return PackageReceipt{}, e
	}
	e = filepath.Walk(st.dir, func(p string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		mode := os.FileMode(0400)
		if info.IsDir() || info.Mode()&0111 != 0 {
			mode = 0500
		}
		return os.Chmod(p, mode)
	})
	if e != nil {
		return PackageReceipt{}, e
	}
	if e = ctx.Err(); e != nil {
		return PackageReceipt{}, e
	}
	if e = os.Rename(st.dir, dest); e != nil {
		return PackageReceipt{}, e
	}
	st.used = true
	return receipt, nil
}
