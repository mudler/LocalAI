// Package ociindex fetches an index file published as an OCI artifact.
//
// It is the one place that resolves, verifies, pulls, validates and caches
// such an artifact, so every catalog that can be an oci:// source (the model
// and backend galleries, the add-on libraries) gets the same credentials,
// signature verification and caching. It imports only pkg/oci so that any
// catalog can use it without depending on another catalog.
package ociindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/xlog"
)

// Options says what to fetch and how far to trust it.
type Options struct {
	// ArtifactType is the artifactType the manifest must declare. Required:
	// the puller refuses anything else, so a plain container image cannot be
	// unpacked and read as an index.
	ArtifactType string

	// IndexFile is the layer title the index is published under. The rest of
	// the tree is kept next to it, because entries may resolve against it.
	IndexFile string

	// CacheDir is where the unpacked artifact lives between fetches. Its
	// parent is also where the staging directory is created, so the final
	// rename stays on one filesystem. Required.
	CacheDir string

	// CacheTTL is how long an unpacked copy is served without asking the
	// registry again. Zero or less means that every fetch asks the registry.
	CacheTTL time.Duration

	// MaxLayers and MaxBytes cap what a hostile or mistaken publisher can
	// make a machine write to disk. Zero means the puller's own defaults.
	MaxLayers int
	MaxBytes  int64

	// Usable reports whether the index bytes are worth serving. It runs on
	// pulled bytes and on a cached copy, so a directory truncated by a full
	// disk or edited by hand is not served. Nil accepts any bytes.
	Usable func([]byte) bool

	// Verify checks the publisher signature over a digest-pinned reference.
	// Nil means no signature check. When set, the reference is resolved to a
	// digest first, verified, and that same digest is pulled, so the
	// verification binds to the bytes that land on disk.
	Verify func(ctx context.Context, digestRef string) error
}

// VerifyError reports that Verify failed. It wraps the verifier's error
// unchanged, so the caller can tell a refusal from an outage with errors.Is
// or errors.As on the verifier's own error types.
type VerifyError struct{ Err error }

func (e *VerifyError) Error() string { return e.Err.Error() }
func (e *VerifyError) Unwrap() error { return e.Err }

// Fetch returns the index of the artifact at ref (an OCI reference without the
// oci:// scheme).
//
// A fresh cached copy is served without touching the registry. Otherwise the
// pull lands in a staging directory and is renamed into CacheDir only once
// the whole tree is on disk and the index passes Usable. The puller writes
// its layers with O_EXCL, so a directory holding partial files from an
// interrupted attempt would fail every later pull, and a partial tree served
// later would hand the user a truncated index with no sign of it.
//
// Fetch never deletes a good cache entry because a refresh failed: the entry
// is replaced only after a new, usable pull is in hand. A caller that wants to
// answer a registry outage with the last good copy can read CacheDir itself.
func Fetch(ctx context.Context, ref string, o Options) ([]byte, error) {
	if o.CacheDir == "" {
		return nil, fmt.Errorf("no cache directory given for %q", ref)
	}
	if body, ok := readCached(o); ok {
		return body, nil
	}

	pullRef := ref
	if o.Verify != nil {
		// Resolve first, verify the digest, then pull that same digest.
		// Nothing has been fetched beyond the manifest at this point, so a
		// policy failure leaves no content anywhere.
		digestRef, err := oci.ResolveArtifactDigestRef(ctx, ref)
		if err != nil {
			return nil, err
		}
		if err := o.Verify(ctx, digestRef); err != nil {
			return nil, &VerifyError{Err: err}
		}
		pullRef = digestRef
	}

	if err := os.MkdirAll(filepath.Dir(o.CacheDir), 0o750); err != nil {
		return nil, fmt.Errorf("could not create the cache directory: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(o.CacheDir), ".staging-*")
	if err != nil {
		return nil, fmt.Errorf("could not stage the artifact: %w", err)
	}
	// Removed on every path but the successful rename, which moves the
	// directory out from under this call.
	defer func() {
		if rmErr := os.RemoveAll(staging); rmErr != nil && !os.IsNotExist(rmErr) {
			xlog.Debug("could not clean up an OCI index staging directory", "path", staging, "error", rmErr)
		}
	}()

	pullOpts := []oci.ArtifactPullOption{oci.WithArtifactType(o.ArtifactType)}
	if o.MaxLayers > 0 {
		pullOpts = append(pullOpts, oci.WithMaxArtifactLayers(o.MaxLayers))
	}
	if o.MaxBytes > 0 {
		pullOpts = append(pullOpts, oci.WithMaxArtifactBytes(o.MaxBytes))
	}
	if _, err := oci.PullArtifact(ctx, pullRef, staging, pullOpts...); err != nil {
		return nil, err
	}

	// #nosec G304 -- staging is this function's own temporary directory and
	// the file name is the caller's constant.
	body, err := os.ReadFile(filepath.Join(staging, o.IndexFile))
	if err != nil {
		return nil, fmt.Errorf("artifact %q carries no %s: %w", ref, o.IndexFile, err)
	}
	if o.Usable != nil && !o.Usable(body) {
		return nil, fmt.Errorf("artifact %q has an %s that is not a usable index", ref, o.IndexFile)
	}

	// The bytes are already in hand, so a rename that loses a race with
	// another fetch, or fails on a read-only cache, costs nothing but the next
	// fetch pulling again. The caller still gets this index.
	if err := os.RemoveAll(o.CacheDir); err != nil {
		xlog.Debug("could not clear a stale OCI index cache entry", "path", o.CacheDir, "error", err)
	} else if err := os.Rename(staging, o.CacheDir); err != nil {
		xlog.Debug("could not install the OCI index cache entry", "path", o.CacheDir, "error", err)
	}

	return body, nil
}

// readCached returns the cached index, if the cache holds one that is fresh
// and usable.
//
// The directory only exists because a completed pull was renamed into place,
// so its presence already means the content was verified before it landed.
// The index is still probed: a truncated or hand-edited directory must not be
// served as if it were the index.
func readCached(o Options) ([]byte, bool) {
	info, err := os.Stat(o.CacheDir)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	// A zero TTL is checked on its own: with a coarse clock (Windows), a
	// copy written in the same tick is zero seconds old, and 0 > 0 is false.
	if o.CacheTTL <= 0 || time.Since(info.ModTime()) > o.CacheTTL {
		return nil, false
	}
	// #nosec G304 -- CacheDir is the caller's own construction and the file
	// name is the caller's constant.
	body, err := os.ReadFile(filepath.Join(o.CacheDir, o.IndexFile))
	if err != nil || (o.Usable != nil && !o.Usable(body)) {
		return nil, false
	}
	return body, true
}
