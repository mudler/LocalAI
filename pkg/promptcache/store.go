package promptcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Key names one file of the tier, relative to the root of a store.
type Key struct {
	// Backend is the backend directory, for example llama-cpp.
	Backend string `json:"backend"`
	// ModelDir is the model directory, <pin16>-<model16>.
	ModelDir string `json:"model_dir"`
	// Name is the file name in the model directory.
	Name string `json:"name"`
}

// Entry is one cache file of the tier.
type Entry struct {
	Key
	// Model16 is the model id of the model directory.
	Model16 string `json:"model16"`
	// NSID is the namespace id of the file.
	NSID string `json:"nsid"`
	Size int64  `json:"size"`
	// LastUse is the mtime of the file: the backend touches a file when it
	// loads it.
	LastUse time.Time `json:"last_use"`
	// Partial is true for a write in progress (.cache.tmp).
	Partial bool `json:"partial,omitempty"`
}

// Store is where the files of the tier live. The janitor, the quotas, the
// purges and the usage report only go through it. The node-local disk
// (DiskStore) is the one implementation.
type Store interface {
	// Stat sums the cache files.
	Stat(ctx context.Context) (Usage, error)
	// List gives the cache files of one namespace id, or of all of them
	// for "". Metadata files are never listed.
	List(ctx context.Context, nsid string) ([]Entry, error)
	// Meta gives the metadata files of the model directories (manifest.json,
	// .speed, .lock), which only a whole-model or whole-cache purge deletes.
	Meta(ctx context.Context) ([]Key, error)
	Get(ctx context.Context, key Key) (io.ReadCloser, error)
	Put(ctx context.Context, key Key, r io.Reader) error
	// Delete removes one file; a file that is already gone is no error.
	Delete(ctx context.Context, key Key) error
}

// The metadata files the backend keeps in a model directory.
const (
	ManifestName = "manifest.json"
	SpeedName    = ".speed"
	LockName     = ".lock"
)

var (
	backendDirRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	modelDirRE   = regexp.MustCompile(`^([0-9a-f]{16})-([0-9a-f]{16})$`)
	fileRE       = regexp.MustCompile(`^([0-9a-f]{16})-([0-9a-f]{16})-([0-9a-f]{32})\.cache(\.tmp)?$`)
	hex16RE      = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// ErrBadKey means a key names no file of the layout.
var ErrBadKey = errors.New("promptcache: the key names no file of the cache layout")

// DiskStore is the tier on a local directory:
//
//	<root>/<backend>/<pin16>-<model16>/<compat16>-<ns16>-<hash32>.cache
//	<root>/<backend>/<pin16>-<model16>/{manifest.json,.speed,.lock}
type DiskStore struct{ Root string }

// NewDiskStore returns the store of root.
func NewDiskStore(root string) *DiskStore { return &DiskStore{Root: root} }

// path checks key against the layout and returns its file path. Any other
// name is refused, so the store never touches a foreign file.
func (d *DiskStore) path(k Key) (string, error) {
	if !backendDirRE.MatchString(k.Backend) || !modelDirRE.MatchString(k.ModelDir) {
		return "", ErrBadKey
	}
	switch {
	case fileRE.MatchString(k.Name), k.Name == ManifestName, k.Name == SpeedName, k.Name == LockName:
	default:
		return "", ErrBadKey
	}
	return filepath.Join(d.Root, k.Backend, k.ModelDir, k.Name), nil
}

// walk calls fn for every model directory of the layout.
func (d *DiskStore) walk(fn func(backend, modelDir, model16, dir string)) error {
	backends, err := os.ReadDir(d.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, b := range backends {
		if !b.IsDir() || !backendDirRE.MatchString(b.Name()) {
			continue
		}
		models, err := os.ReadDir(filepath.Join(d.Root, b.Name()))
		if err != nil {
			continue
		}
		for _, m := range models {
			mm := modelDirRE.FindStringSubmatch(m.Name())
			if !m.IsDir() || mm == nil {
				continue
			}
			fn(b.Name(), m.Name(), mm[2], filepath.Join(d.Root, b.Name(), m.Name()))
		}
	}
	return nil
}

// List implements Store.
func (d *DiskStore) List(ctx context.Context, nsid string) ([]Entry, error) {
	var out []Entry
	err := d.walk(func(backend, modelDir, model16, dir string) {
		if ctx.Err() != nil {
			return
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, f := range files {
			fm := fileRE.FindStringSubmatch(f.Name())
			if fm == nil || !f.Type().IsRegular() || (nsid != "" && fm[2] != nsid) {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			out = append(out, Entry{
				Key:     Key{Backend: backend, ModelDir: modelDir, Name: f.Name()},
				Model16: model16, NSID: fm[2], Size: info.Size(), LastUse: info.ModTime(), Partial: fm[4] != "",
			})
		}
	})
	if err == nil {
		err = ctx.Err()
	}
	return out, err
}

// Meta implements Store.
func (d *DiskStore) Meta(ctx context.Context) ([]Key, error) {
	var out []Key
	err := d.walk(func(backend, modelDir, _, dir string) {
		for _, name := range []string{ManifestName, SpeedName, LockName} {
			if info, err := os.Lstat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
				out = append(out, Key{Backend: backend, ModelDir: modelDir, Name: name})
			}
		}
	})
	if err == nil {
		err = ctx.Err()
	}
	return out, err
}

// Stat implements Store.
func (d *DiskStore) Stat(ctx context.Context) (Usage, error) {
	entries, err := d.List(ctx, "")
	if err != nil {
		return Usage{Models: map[string]Usage{}, Namespaces: map[string]Usage{}}, err
	}
	return sum(entries), nil
}

// Get implements Store.
func (d *DiskStore) Get(_ context.Context, k Key) (io.ReadCloser, error) {
	p, err := d.path(k)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Put implements Store: it writes a temporary file and renames it, so a
// reader never sees half a file.
func (d *DiskStore) Put(_ context.Context, k Key, r io.Reader) error {
	p, err := d.path(k)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("promptcache: write %s: %w", k.Name, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// Delete implements Store.
func (d *DiskStore) Delete(_ context.Context, k Key) error {
	p, err := d.path(k)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
