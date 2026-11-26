package content

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// StagingDir is the folder under the packages root where a writer builds a
// package before it moves it to its final name.
const StagingDir = ".staging"

// ErrDigestMismatch means the files are not the files of the digest.
var ErrDigestMismatch = errors.New("plugin content does not match its digest")

// Workspace is the workspace store that holds the packages, with the actor
// that writes them (the workspace administrator).
type Workspace struct {
	Files workspace.Store
	Actor workspace.Actor
}

func packagePath(root, digest string) (string, error) {
	if !ValidDigest(digest) {
		return "", fmt.Errorf("%w: %q is not a content digest", ErrRefused, digest)
	}
	return root + "/" + DigestHex(digest), nil
}

// Exists reports whether the folder of a package is in the store.
func Exists(ctx context.Context, ws Workspace, root, digest string) (bool, error) {
	p, err := packagePath(root, digest)
	if err != nil {
		return false, err
	}
	_, err = ws.Files.Stat(ctx, ws.Actor, p)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, workspace.ErrNotFound):
		return false, nil
	}
	return false, err
}

// Write stores the tree t, read from the folder dir, as the package digest
// under root, unless the package is there already. It writes into a staging
// folder, checks the hash of every file against t, then moves the folder to
// its final name. When another writer moved the same digest first, Write
// drops its own copy and succeeds: the content is the same.
func Write(ctx context.Context, ws Workspace, root, digest, dir string, t Tree) error {
	final, err := packagePath(root, digest)
	if err != nil {
		return err
	}
	if Digest(t) != digest {
		return fmt.Errorf("%w: the tree has another digest", ErrDigestMismatch)
	}
	if ok, err := Exists(ctx, ws, root, digest); err != nil || ok {
		return err
	}
	src, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer src.Close()

	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	stage := root + "/" + StagingDir + "/" + hex.EncodeToString(nonce[:])
	if err := stageFiles(ctx, ws, src, stage, t); err != nil {
		removeStage(ctx, ws, stage)
		return err
	}
	if _, err := ws.Files.Move(ctx, ws.Actor, stage, final); err != nil {
		removeStage(ctx, ws, stage)
		if errors.Is(err, workspace.ErrExists) {
			return nil
		}
		return fmt.Errorf("plugin content: move %s: %w", DigestHex(digest), err)
	}
	return nil
}

// stageFiles writes the files into stage in one change set and checks what
// the store kept.
func stageFiles(ctx context.Context, ws Workspace, src *os.Root, stage string, t Tree) error {
	tx, err := ws.Files.Begin(ctx, ws.Actor, "Stage a plugin package")
	if err != nil {
		return err
	}
	if _, err := tx.Mkdir(ctx, stage); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, f := range t.Files {
		data, err := readChecked(src, f)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.Put(ctx, stage+"/"+f.Path, bytes.NewReader(data), workspace.PutOptions{}); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("plugin content: stage %s: %w", f.Path, err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return err
	}
	for _, f := range t.Files {
		n, err := ws.Files.Stat(ctx, ws.Actor, stage+"/"+f.Path)
		if err != nil {
			return err
		}
		if n.Blob != f.SHA256 || n.Size != f.Size {
			return fmt.Errorf("%w: the store kept other bytes for %s", ErrDigestMismatch, f.Path)
		}
	}
	return nil
}

// readChecked reads one file of the tree and checks its size and hash: the
// folder may have changed since Check.
func readChecked(src *os.Root, f File) ([]byte, error) {
	fh, err := src.Open(filepath.FromSlash(f.Path))
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, f.Size+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != f.Size || hex.EncodeToString(sum[:]) != f.SHA256 {
		return nil, fmt.Errorf("%w: %s changed after the check", ErrDigestMismatch, f.Path)
	}
	return data, nil
}

func removeStage(ctx context.Context, ws Workspace, stage string) {
	// A cancelled request must not leave its staging folder behind; the GC
	// removes what is left anyway.
	_ = ws.Files.Delete(context.WithoutCancel(ctx), ws.Actor, stage)
}

// Open reads one file of a package. p is relative to the package folder.
func Open(ctx context.Context, ws Workspace, root, digest, p string) (io.ReadSeekCloser, error) {
	dir, err := packagePath(root, digest)
	if err != nil {
		return nil, err
	}
	if err := CheckRelPath(p); err != nil {
		return nil, err
	}
	r, _, err := ws.Files.Open(ctx, ws.Actor, dir+"/"+p)
	return r, err
}

// Remove deletes the folder of a package. A missing folder is not an error.
func Remove(ctx context.Context, ws Workspace, root, digest string) error {
	p, err := packagePath(root, digest)
	if err != nil {
		return err
	}
	if err := ws.Files.Delete(ctx, ws.Actor, p); err != nil && !errors.Is(err, workspace.ErrNotFound) {
		return err
	}
	return nil
}
