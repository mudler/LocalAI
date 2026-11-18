package p2pnet

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/mudler/xlog"
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
	b, err := os.ReadFile(path)
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
// keeps the old key next to it as path + ".prev". Every peer that pinned the
// old peer ID must pair again.
func RegenerateIdentity(path string) (crypto.PrivKey, error) {
	if path == "" {
		return nil, errors.New("p2pnet: identity path is empty")
	}
	k, tmp, err := stageIdentity(path)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(path, path+".prev"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("p2pnet: keep previous identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("p2pnet: install identity: %w", err)
	}
	return k, nil
}

func writeNewIdentity(path string) (crypto.PrivKey, error) {
	k, tmp, err := stageIdentity(path)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("p2pnet: install identity: %w", err)
	}
	return k, nil
}

// stageIdentity writes a new key to a temporary file in the directory of
// path, so that the final rename is atomic and a crash never leaves a
// half-written key at path.
func stageIdentity(path string) (crypto.PrivKey, string, error) {
	k, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("p2pnet: generate identity: %w", err)
	}
	b, err := crypto.MarshalPrivateKey(k)
	if err != nil {
		return nil, "", fmt.Errorf("p2pnet: encode identity: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("p2pnet: create identity directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".identity-*")
	if err != nil {
		return nil, "", fmt.Errorf("p2pnet: write identity: %w", err)
	}
	tmp := f.Name()
	fail := func(err error) (crypto.PrivKey, string, error) {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, "", fmt.Errorf("p2pnet: write identity: %w", err)
	}
	if err := f.Chmod(identityMode); err != nil {
		return fail(err)
	}
	if _, err := f.Write(b); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, "", fmt.Errorf("p2pnet: write identity: %w", err)
	}
	return k, tmp, nil
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
