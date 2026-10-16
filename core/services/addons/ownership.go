// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// OwnerConfig is deployment configuration, never request-supplied authority.
// Shared filesystems and automatic reassignment are intentionally unsupported.
type OwnerConfig struct {
	InstanceID, OwnerID, OwnerAddress, StorageID, Root string
	SharedStorage                                      bool
}

func (c OwnerConfig) validate() error {
	u, e := url.Parse(c.OwnerAddress)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || c.InstanceID == "" || c.OwnerID == "" || c.StorageID == "" || !filepath.IsAbs(c.Root) || c.SharedStorage {
		return ErrConfiguration
	}
	return nil
}

type OwnerGuard struct{ Config OwnerConfig }

func (g OwnerGuard) Acquire(ctx context.Context) (func() error, error) {
	c := g.Config
	if e := c.validate(); e != nil {
		return nil, e
	}
	if c.InstanceID != c.OwnerID {
		return nil, ErrNotOwner
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(c.Root, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(c.Root)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrConfiguration
	}
	f, e := os.OpenFile(filepath.Join(c.Root, "owner.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, ErrOwnerBusy
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() { releaseErr = errors.Join(syscall.Flock(int(f.Fd()), syscall.LOCK_UN), f.Close()) })
		return releaseErr
	}
	identity := struct{ OwnerID, Address, StorageID string }{c.OwnerID, c.OwnerAddress, c.StorageID}
	expected, _ := json.Marshal(identity)
	p := filepath.Join(c.Root, "identity.json")
	b, e := os.ReadFile(p) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if os.IsNotExist(e) {
		e = durableWrite(p, expected)
	} else if e == nil && string(b) != string(expected) {
		e = ErrConfiguration
	}
	if e != nil {
		_ = release()
		return nil, e
	}
	return release, nil
}
