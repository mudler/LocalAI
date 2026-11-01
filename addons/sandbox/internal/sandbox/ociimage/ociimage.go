package ociimage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// LayoutPrefix marks a reference to an image in a local OCI layout folder:
// "oci-layout:<folder>@sha256:<hex>". Tests and CI use it; products use
// registry references.
const LayoutPrefix = "oci-layout:"

// Resolve checks that ref names a digest and returns the digest and a loader
// for the image. A reference without a digest is refused.
func Resolve(ctx context.Context, ref string) (string, func() (v1.Image, error), error) {
	if rest, ok := strings.CutPrefix(ref, LayoutPrefix); ok {
		at := strings.LastIndex(rest, "@")
		if at < 0 {
			return "", nil, fmt.Errorf("ociimage: image %q has no digest", ref)
		}
		dir, digest := rest[:at], rest[at+1:]
		h, err := v1.NewHash(digest)
		if err != nil {
			return "", nil, fmt.Errorf("ociimage: image %q: %w", ref, err)
		}
		return h.String(), func() (v1.Image, error) {
			return LayoutImage(dir, h)
		}, nil
	}
	r, err := name.ParseReference(ref)
	if err != nil {
		return "", nil, fmt.Errorf("ociimage: image %q: %w", ref, err)
	}
	d, ok := r.(name.Digest)
	if !ok {
		return "", nil, fmt.Errorf("ociimage: image %q has no digest; use name@sha256:...", ref)
	}
	return d.DigestStr(), func() (v1.Image, error) {
		return remote.Image(d, remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain),
			remote.WithPlatform(v1.Platform{OS: "linux", Architecture: runtime.GOARCH}))
	}, nil
}

// Key is the cache folder name of an image: its digest with ":" replaced by
// "-", then the architecture of this process. The key has no part for the
// backend, and the backends put different files in the folder (the env.json
// that marks it complete is the same). Use one cache folder per backend: a
// backend that finds a folder another backend built reads it as complete and
// then misses its own files.
func Key(digest string) string {
	return strings.ReplaceAll(digest, ":", "-") + "-" + runtime.GOARCH
}

// Prepare returns the cache folder of ref under cacheDir and the environment
// of the image. When the cache does not have the image, Prepare loads it and
// calls build to fill the folder; build must not write env.json. Two callers
// that prepare the same digest at once share one build through a lock file.
// Prepare writes env.json last, which marks the folder complete.
func Prepare(ctx context.Context, cacheDir, ref string, build func(img v1.Image, dir string) error) (dir string, env []string, err error) {
	digest, load, err := Resolve(ctx, ref)
	if err != nil {
		return "", nil, err
	}
	key := Key(digest)
	dir = filepath.Join(cacheDir, key)
	if env, err := ReadEnv(dir); err == nil {
		return dir, env, nil
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", nil, err
	}
	unlock, err := LockFile(ctx, filepath.Join(cacheDir, key+".lock"))
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	if env, err := ReadEnv(dir); err == nil { // built while we waited
		return dir, env, nil
	}
	img, err := load()
	if err != nil {
		return "", nil, fmt.Errorf("ociimage: pull %s: %w", ref, err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return "", nil, fmt.Errorf("ociimage: %s: %w", ref, err)
	}
	// A folder left by a build that did not finish has no env.json. Start
	// from an empty one.
	if err := RemoveTree(dir); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	if err := build(img, dir); err != nil {
		// A build can leave folders without owner write; clear them so the
		// next try starts clean.
		_ = RemoveTree(dir)
		return "", nil, fmt.Errorf("ociimage: unpack %s: %w", ref, err)
	}
	// env.json marks the folder complete, so only Prepare writes it. A build
	// that wrote one could pass off a half-built folder as a finished one.
	if _, err := os.Lstat(filepath.Join(dir, "env.json")); err == nil {
		_ = RemoveTree(dir)
		return "", nil, fmt.Errorf("ociimage: unpack %s: the build wrote env.json, which only Prepare may write", ref)
	}
	env = cfg.Config.Env
	if env == nil {
		env = []string{}
	}
	b, err := json.Marshal(env)
	if err != nil {
		return "", nil, err
	}
	if err := WriteSync(filepath.Join(dir, "env.json.tmp"), b); err != nil {
		return "", nil, err
	}
	if err := os.Rename(filepath.Join(dir, "env.json.tmp"), filepath.Join(dir, "env.json")); err != nil {
		return "", nil, err
	}
	return dir, env, SyncDir(dir)
}

// RemoveTree deletes dir and everything below it, also when a build left
// folders that the owner cannot write to (an image can contain a 0555 folder).
// When the first try fails, it makes every folder owner-accessible and tries
// again.
func RemoveTree(dir string) error {
	err := os.RemoveAll(dir)
	if err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			_ = os.Chmod(p, 0o700) // #nosec G122 G302 -- makes a folder of the add-on cache removable by its owner
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// PrepareTar is Prepare for a backend that unpacks the flattened layer tar
// itself. build gets the tar stream and the empty cache folder. A backend
// that only needs this does not import go-containerregistry. An error that
// build returns appears as an "unpack" error, also when the stream itself
// failed to read (a damaged layer or a pull that broke).
func PrepareTar(ctx context.Context, cacheDir, ref string, build func(tar io.Reader, dir string) error) (dir string, env []string, err error) {
	return Prepare(ctx, cacheDir, ref, func(img v1.Image, dir string) error {
		rc := Extract(img)
		defer func() { _ = rc.Close() }()
		return build(rc, dir)
	})
}

// ReadEnv reads the environment that Prepare stored in dir. An error means
// the folder is not complete.
func ReadEnv(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "env.json")) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return nil, err
	}
	var env []string
	return env, json.Unmarshal(b, &env)
}

// Extract returns the flattened layers of img as one tar stream. The caller
// closes it.
func Extract(img v1.Image) io.ReadCloser { return mutate.Extract(img) }

// WriteSync writes b to p and flushes it to disk before it returns.
func WriteSync(p string, b []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// SyncDir flushes the entries of dir, so the renames in it survive a crash.
func SyncDir(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// CheckEntryName refuses an entry name that is absolute or leaves the image.
// A backend that writes the names as paths in a folder must call it for every
// entry: a layer that carries one is not a valid image.
func CheckEntryName(name string) error {
	c := path.Clean(strings.TrimPrefix(name, "./"))
	if path.IsAbs(name) || c == ".." || strings.HasPrefix(c, "../") {
		return fmt.Errorf("the image has an unsafe entry name %q", name)
	}
	return nil
}

// ErrNoLock means the host cannot lock files.
var ErrNoLock = errors.New("ociimage: the image cache lock needs a Unix host")
