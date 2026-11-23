package p2pnet

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// identityMode is the file mode of an identity key: the key is the peer's
// secret, so only the owner may read it.
const identityMode fs.FileMode = 0o600

// LoadOrCreateIdentity reads the libp2p private key at path. When the file
// does not exist, it creates an ed25519 key and writes it with mode 0600,
// creating the directory with mode 0700 when needed. A file that exists but
// cannot be parsed is an error and stays untouched, because replacing it
// would silently change the peer ID that paired peers know.
func LoadOrCreateIdentity(path string) (crypto.PrivKey, error) {
	if path == "" {
		return nil, errors.New("p2pnet: identity path is empty")
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path is the identity file the operator chose, not a request value
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return writeNewIdentity(path)
	case err != nil:
		return nil, fmt.Errorf("p2pnet: read identity: %w", err)
	}
	k, err := crypto.UnmarshalPrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("p2pnet: parse identity %s: %w", path, err)
	}
	if err := restrictIdentityMode(path); err != nil {
		return nil, err
	}
	return k, nil
}

// RegenerateIdentity replaces the key at path with a new ed25519 key and
// keeps the old key next to it as path + ".prev". A ".prev" that is already
// there is an older key: it is kept as path + ".prev.<unix time>" and is never
// overwritten. Every peer that pinned the old peer ID must pair again.
func RegenerateIdentity(path string) (crypto.PrivKey, error) {
	if path == "" {
		return nil, errors.New("p2pnet: identity path is empty")
	}
	k, b, err := newIdentity()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("p2pnet: create identity directory: %w", err)
	}
	prev := path + ".prev"
	if old, err := os.ReadFile(path); err == nil { // #nosec G304 -- path is the identity file the operator chose, not a request value
		if _, err := os.Stat(prev); err == nil {
			older := fmt.Sprintf("%s.%d", prev, time.Now().Unix())
			if err := os.Rename(prev, older); err != nil {
				return nil, fmt.Errorf("p2pnet: keep the older identity backup: %w", err)
			}
		}
		// A copy, so that the key at path never goes missing.
		if err := atomicfile.Write(prev, old, identityMode); err != nil {
			return nil, fmt.Errorf("p2pnet: keep previous identity: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("p2pnet: keep previous identity: %w", err)
	}
	if err := atomicfile.Write(path, b, identityMode); err != nil {
		return nil, fmt.Errorf("p2pnet: install identity: %w", err)
	}
	return k, nil
}

func writeNewIdentity(path string) (crypto.PrivKey, error) {
	k, b, err := newIdentity()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("p2pnet: create identity directory: %w", err)
	}
	if err := atomicfile.Write(path, b, identityMode); err != nil {
		return nil, fmt.Errorf("p2pnet: install identity: %w", err)
	}
	return k, nil
}

// newIdentity makes a new ed25519 key and its encoding.
func newIdentity() (crypto.PrivKey, []byte, error) {
	k, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("p2pnet: generate identity: %w", err)
	}
	b, err := crypto.MarshalPrivateKey(k)
	if err != nil {
		return nil, nil, fmt.Errorf("p2pnet: encode identity: %w", err)
	}
	return k, b, nil
}

// restrictIdentityMode removes group and other permissions from a key file
// that was copied or restored with a wider mode.
func restrictIdentityMode(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("p2pnet: stat identity: %w", err)
	}
	if st.Mode().Perm()&0o077 == 0 {
		return nil
	}
	xlog.Warn("P2P identity key was readable by other users; restricting it to the owner", "path", path, "mode", st.Mode().Perm().String())
	if err := os.Chmod(path, identityMode); err != nil {
		return fmt.Errorf("p2pnet: restrict identity mode: %w", err)
	}
	return nil
}
