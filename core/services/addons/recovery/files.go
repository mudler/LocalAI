// SPDX-License-Identifier: MIT
package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3" // the driver of the backup and the integrity check
)

// FileEntry is one file of a backup.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// SQLite marks a database file that was copied with VACUUM INTO and
	// checked with PRAGMA integrity_check.
	SQLite bool `json:"sqlite,omitempty"`
	// UserVersion is the schema version a SQLite file carried.
	UserVersion int64 `json:"userVersion,omitempty"`
}

var sqliteMagic = []byte("SQLite format 3\x00")

// scratchDir names directories of an add-on's data that hold temporary files
// the add-on rebuilds itself: staged exports and scratch space. They are not
// part of a backup.
func scratchDir(name string) bool { return name == "exports" || name == "tmp" }

func isSQLite(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, len(sqliteMagic))
	if _, err = io.ReadFull(f, b); err != nil {
		return false
	}
	return string(b) == string(sqliteMagic)
}

func sqliteDSN(path, mode string) string {
	esc := url.PathEscape(path)
	// PathEscape leaves ? and # escaped, which is what the URI form needs.
	return "file:" + strings.ReplaceAll(esc, "%2F", "/") + "?mode=" + mode + "&_busy_timeout=5000"
}

// vacuumInto writes a consistent standalone copy of the database at src. The
// source is opened read-only, so a hot WAL is read and never rewritten.
func vacuumInto(ctx context.Context, src, dst string) error {
	db, err := sql.Open("sqlite3", sqliteDSN(src, "ro"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Chmod(dst, 0o600)
}

// checkSQLite opens a database read-only and runs the integrity check. It
// returns the schema version of the file.
func checkSQLite(ctx context.Context, path string) (int64, error) {
	db, err := sql.Open("sqlite3", sqliteDSN(path, "ro"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	ok := false
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			return 0, err
		}
		if s != "ok" {
			return 0, fmt.Errorf("%w: %s", ErrBackupCorrupt, s)
		}
		ok = true
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrBackupCorrupt
	}
	var v int64
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func syncPath(path string) error {
	f, err := os.Open(path) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return err
	}
	err = f.Sync()
	return errors.Join(err, f.Close())
}

// syncTree makes every file and directory below root durable.
func syncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		return syncPath(p)
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err = syncPath(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func copyPlain(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}

// sizeOf adds up the regular files below root, for the storage estimate. A
// missing root is empty. Scratch directories are not counted.
func sizeOf(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			if p != root && scratchDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// copyData copies the data directory src to dst (which must not exist) and
// returns the entries, relative to dst, in path order. SQLite files are
// written with VACUUM INTO, so the copy holds no WAL. Symbolic links and
// scratch directories are skipped; the count of skipped links is returned.
func copyData(ctx context.Context, src, dst, prefix string, step func() error) ([]FileEntry, int, error) {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return nil, 0, err
	}
	var out []FileEntry
	skipped := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			skipped++
			return nil
		case d.IsDir():
			if scratchDir(d.Name()) {
				return fs.SkipDir
			}
			return os.MkdirAll(target, 0o700)
		case !d.Type().IsRegular():
			skipped++
			return nil
		}
		name := d.Name()
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if base, ok := strings.CutSuffix(p, suffix); ok && strings.HasSuffix(name, suffix) {
				if info, e := os.Lstat(base); e == nil && info.Mode().IsRegular() && isSQLite(base) {
					return nil
				}
			}
		}
		if step != nil {
			if err = step(); err != nil {
				return err
			}
		}
		entry := FileEntry{Path: filepath.ToSlash(filepath.Join(prefix, rel))}
		if isSQLite(p) {
			if err = vacuumInto(ctx, p, target); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			entry.SQLite = true
		} else if err = copyPlain(p, target); err != nil {
			return err
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, skipped, nil
}

// describe fills the digest, size and schema version of entries that were
// written below base (entry paths are relative to base).
func describe(ctx context.Context, base string, entries []FileEntry) error {
	for i := range entries {
		p := filepath.Join(base, filepath.FromSlash(entries[i].Path))
		sum, n, err := hashFile(p)
		if err != nil {
			return err
		}
		entries[i].SHA256, entries[i].Size = sum, n
		if entries[i].SQLite {
			v, err := checkSQLite(ctx, p)
			if err != nil {
				return err
			}
			entries[i].UserVersion = v
		}
	}
	return nil
}

// removeAll deletes a tree that may hold read-only files or directories.
func removeAll(path string) error {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700) // #nosec G122,G302 -- the tree is owned by this process and locked; the walk only normalises its own files
		}
		return nil
	})
	return os.RemoveAll(path)
}
