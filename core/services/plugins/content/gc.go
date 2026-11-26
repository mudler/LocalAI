package content

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// PackageRetention is how long an unreferenced package stays. A young
// package may belong to an install whose rows are not written yet.
const PackageRetention = 24 * time.Hour

// StagingRetention is how long a staging folder stays. A writer moves its
// folder within one install, so an older one was abandoned.
const StagingRetention = time.Hour

// PackageStore is the part of the plugin store that GC uses.
type PackageStore interface {
	Package(ctx context.Context, digest string) (store.Package, error)
	UnreferencedPackages(ctx context.Context, olderThan time.Time) ([]string, error)
	DeletePackage(ctx context.Context, digest string) (bool, error)
}

// GCResult says what a collection removed.
type GCResult struct {
	// Packages are the digests whose row and folder were deleted.
	Packages []string
	// Orphans are the digests of old folders that had no row.
	Orphans []string
	// Staging is the number of abandoned staging folders deleted.
	Staging int
}

// GC runs Collect under advisorylock.KeyPluginsRefresh, so one frontend
// collects at a time. It reports false when another frontend holds the
// lock. A server without a database (db nil) has one frontend and collects
// at once. A caller that already holds the refresh lock calls Collect: the
// lock is not reentrant.
func GC(ctx context.Context, db *gorm.DB, ws Workspace, root string, st PackageStore, now time.Time) (bool, error) {
	if db == nil {
		_, err := Collect(ctx, ws, root, st, now)
		return err == nil, err
	}
	return advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyPluginsRefresh, func() error {
		_, err := Collect(ctx, ws, root, st, now)
		return err
	})
}

// Collect deletes the packages that no install refers to and that are older
// than PackageRetention, row first and folder second, so a package that an
// install refers to again keeps its files. It also deletes old package
// folders that have no row (an install that failed after the write) and
// abandoned staging folders.
func Collect(ctx context.Context, ws Workspace, root string, st PackageStore, now time.Time) (GCResult, error) {
	var res GCResult
	if _, err := ws.Files.Stat(ctx, ws.Actor, root); errors.Is(err, workspace.ErrNotFound) {
		return res, nil
	} else if err != nil {
		return res, err
	}

	cutoff := now.Add(-PackageRetention)
	unref, err := st.UnreferencedPackages(ctx, cutoff)
	if err != nil {
		return res, err
	}
	for _, d := range unref {
		deleted, err := st.DeletePackage(ctx, d)
		if err != nil {
			return res, err
		}
		if !deleted {
			continue // an install refers to it again
		}
		if err := Remove(ctx, ws, root, d); err != nil {
			return res, err
		}
		res.Packages = append(res.Packages, d)
	}

	children, err := ws.Files.List(ctx, ws.Actor, root)
	if err != nil {
		return res, err
	}
	for _, c := range children {
		d := "sha256:" + c.Name
		if c.Kind != workspace.KindDir || !ValidDigest(d) || !c.Modified.Before(cutoff) {
			continue
		}
		if _, err := st.Package(ctx, d); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			return res, err
		}
		if err := Remove(ctx, ws, root, d); err != nil {
			return res, err
		}
		res.Orphans = append(res.Orphans, d)
	}

	staged, err := ws.Files.List(ctx, ws.Actor, root+"/"+StagingDir)
	if errors.Is(err, workspace.ErrNotFound) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	for _, c := range staged {
		if !c.Modified.Before(now.Add(-StagingRetention)) {
			continue
		}
		if err := ws.Files.Delete(ctx, ws.Actor, c.Path); err != nil && !errors.Is(err, workspace.ErrNotFound) {
			return res, err
		}
		res.Staging++
	}
	return res, nil
}
