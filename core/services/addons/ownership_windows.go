//go:build windows

package addons

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

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
	if !info.IsDir() {
		return nil, ErrConfiguration
	}
	f, e := os.OpenFile(filepath.FromSlash(filepath.Join(c.Root, "owner.lock")), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	path := f.Name()
	lockMu.Lock()
	defer lockMu.Unlock()
	if locks[path] {
		_ = f.Close()
		return nil, ErrOwnerBusy
	}
	locks[path] = true
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() { releaseErr = errors.Join(f.Close()) })
		return releaseErr
	}
	identity := ownerIdentity{OwnerID: c.OwnerID, StorageID: c.StorageID}
	expected, _ := json.Marshal(identity)
	p := filepath.Join(c.Root, "identity.json")
	b, e := os.ReadFile(p) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if os.IsNotExist(e) {
		e = durableWrite(p, expected)
	} else if e == nil {
		var stored ownerIdentity
		if json.Unmarshal(b, &stored) != nil || stored != identity {
			e = ErrConfiguration
		}
	}
	if e != nil {
		_ = release()
		return nil, e
	}
	return release, nil
}

var (
	lockMu sync.Mutex
	locks  = map[string]bool{}
)