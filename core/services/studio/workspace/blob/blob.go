// Package blob stores the contents of workspace files by the SHA-256 of their
// bytes. The workspace keeps names, versions and access in its own store and
// refers to a file's bytes only by this hash, so the bytes can live on a local
// disk or in an object store without the workspace changing.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Store keeps immutable blobs named by the SHA-256 (hex) of their bytes.
type Store interface {
	// Put reads r to its end and stores the bytes. A blob that exists is not
	// written again. Over max bytes (when max is above zero) it stops and
	// returns workspace.ErrTooLarge; an error leaves no blob.
	Put(ctx context.Context, r io.Reader, max int64) (sha string, size int64, err error)
	// Open returns a blob for reading and seeking. An unknown or malformed hash
	// is workspace.ErrNotFound.
	Open(ctx context.Context, sha string) (io.ReadSeekCloser, error)
	// Exists reports whether the blob is stored.
	Exists(ctx context.Context, sha string) (bool, error)
	// LinkTo makes dst a read-only file with the blob's bytes: a hard link when
	// the blob is on the same file system, a copy otherwise. dst must not
	// exist; its folder must. An unknown hash is workspace.ErrNotFound.
	LinkTo(ctx context.Context, sha, dst string) error
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validSHA reports whether s is a lower-case hex SHA-256.
func validSHA(s string) bool { return shaRe.MatchString(s) }

// shaPath is the place of a blob under a root folder: ab/cd/<sha>.
func shaPath(root, sha string) string {
	return filepath.Join(root, sha[0:2], sha[2:4], sha)
}

// ingest copies r into a new temp file under dir while hashing it. The caller
// removes the file. It refuses more than max bytes when max is above zero.
func ingest(r io.Reader, max int64, dir string) (path, sha string, size int64, err error) {
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", "", 0, err
	}
	f, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return "", "", 0, err
	}
	path = f.Name()
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(path)
			path = ""
		}
	}()
	h := sha256.New()
	src := r
	if max > 0 {
		src = io.LimitReader(r, max+1)
	}
	size, err = io.Copy(io.MultiWriter(f, h), src)
	if err != nil {
		return path, "", 0, fmt.Errorf("blob: read the upload: %w", err)
	}
	if max > 0 && size > max {
		return path, "", 0, workspace.ErrTooLarge
	}
	if err = f.Sync(); err != nil {
		return path, "", 0, err
	}
	return path, hex.EncodeToString(h.Sum(nil)), size, nil
}

// place moves a finished temp file to its final path and makes it read-only.
// A blob that is already there stays; the temp file goes.
func place(tmp, final string) error {
	if _, err := os.Stat(final); err == nil {
		return os.Remove(tmp)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o444); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		if _, serr := os.Stat(final); serr == nil && errors.Is(err, os.ErrExist) {
			return os.Remove(tmp)
		}
		return err
	}
	return nil
}

// linkOrCopy makes dst a hard link to src, or a read-only copy when a link is
// not possible, for example across file systems.
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
