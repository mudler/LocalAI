package modelartifacts

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/xlog"
)

const (
	// PartialOrphanTTL is how long an unclaimed partial tree survives before a
	// sweep reclaims it. Writer-unique staging means a crashed writer's tree is
	// never overwritten by its successor, so without reaping, every crash
	// during a large download leaks its bytes onto a shared volume forever.
	//
	// It matches the window the startup reaper already uses for stray
	// *.partial files, and it is orders of magnitude beyond any write-free
	// interval a live writer can have: the download stall guard aborts a silent
	// transfer after DownloadStallTimeout, and the only other gap between
	// writes is hashing one downloaded file.
	PartialOrphanTTL = 24 * time.Hour

	// partialAdoptionIdleWindow is how long a foreign partial tree for the same
	// artifact must have been untouched before this writer takes it over and
	// resumes from it. Without adoption, writer-unique staging would silently
	// cost the resume that a restarted process relies on to finish a
	// multi-tens-of-gigabytes repo.
	//
	// Adoption is normally justified by the artifact lock alone: it only ever
	// runs while this process holds that lock, and flock is released exactly
	// when the owning process dies, so a peer that respects the lock is
	// provably not writing. The idle window is the second line of defence for
	// the case this whole change is about, where the lock fails to exclude.
	//
	// The window is a deliberate compromise in both directions. Too long and a
	// restarted replica re-downloads from zero; too short and a broken lock
	// lets us claim a live peer's tree. Both cost the same thing - one wasted
	// download - because every adopted blob is still SHA-verified before it is
	// promoted, so a wrongly adopted tree fails verification instead of
	// committing corruption.
	partialAdoptionIdleWindow = 5 * time.Minute
)

// newWriterID draws the identity that makes this process run's partial tree its
// own.
//
// It has to be unique across every process that can reach the same models
// volume. That rules out the PID, which repeats freely across containers
// sharing a NAS mount, and the hostname, which a restarted pod can inherit
// while the dead pod's tree is still on disk. 64 bits of crypto/rand depends on
// nothing outside this process; a collision would need two replicas to draw the
// same value in the same 24h window, and even then it degrades to the
// shared-tree behaviour that was the status quo, not to something worse.
func newWriterID() string {
	var raw [8]byte
	// crypto/rand.Read never returns an error; it panics if the system RNG is
	// unusable, which is not a condition worth threading through this API.
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// newestModTime reports the most recent modification anywhere in tree.
//
// The directory's own mtime is not enough: writing bytes into
// `.downloads/<blob>.partial` does not touch any ancestor, so a busy writer's
// top-level directory can look hours old. Reading the whole subtree is what
// makes "is anyone still working here?" answerable. Unreadable entries are
// skipped rather than aborting the walk, but they count as "now" so a tree we
// cannot fully inspect is never judged stale.
func newestModTime(tree string) (time.Time, error) {
	newest := time.Time{}
	err := filepath.WalkDir(tree, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			newest = time.Now()
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			newest = time.Now()
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

// idleFor reports whether tree has seen no modification for at least window.
func idleFor(tree string, window time.Duration) bool {
	newest, err := newestModTime(tree)
	if err != nil {
		return false
	}
	return time.Since(newest) >= window
}

// removePartialTree deletes one staging tree, refusing any path that is not a
// direct, correctly named child of the partial root. Writer-unique staging
// turned a self-healing overwrite into an explicit delete, and an explicit
// delete on a shared models volume is worth being paranoid about.
func removePartialTree(partialRoot, tree string) error {
	name := filepath.Base(tree)
	if filepath.Dir(tree) != partialRoot {
		return fmt.Errorf("refusing to remove partial tree outside %q", partialRoot)
	}
	if _, _, ok := splitPartialDirName(name); !ok {
		return fmt.Errorf("refusing to remove unrecognised partial tree %q", name)
	}
	root, err := os.OpenRoot(partialRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.RemoveAll(name)
}

// adoptOrphanPartial hands this writer a dead peer's staging tree for the same
// artifact, so a download interrupted by a crash or a restart resumes instead
// of starting over.
//
// The claim is a rename, which is atomic: two writers racing for the same
// orphan cannot both win, and the loser simply starts fresh. A failure to adopt
// is never fatal - it costs bytes, not correctness - so every error here is
// logged and swallowed.
func adoptOrphanPartial(layout Layout) {
	entries, err := os.ReadDir(layout.PartialRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		candidate := filepath.Join(layout.PartialRoot, name)
		if candidate == layout.Partial {
			continue
		}
		cacheKey, _, ok := splitPartialDirName(name)
		if !ok || cacheKey != layout.CacheKey {
			continue
		}
		if !idleFor(candidate, partialAdoptionIdleWindow) {
			continue
		}
		if err := os.Rename(candidate, layout.Partial); err != nil {
			xlog.Debug("could not adopt an abandoned artifact partial", "partial", candidate, "error", err)
			continue
		}
		xlog.Info("resuming an abandoned artifact download", "partial", candidate, "adopted-as", layout.Partial)
		return
	}
}

// SweepStalePartialTrees reclaims staging trees left behind by writers that
// never finished, returning how many it removed.
//
// A tree is removed only when its name is one this package writes, it is not
// the caller's own, and nothing anywhere inside it has been touched for
// olderThan. That last condition is what makes the sweep safe against a live
// writer: a running download writes continuously, so its newest mtime is
// seconds old, and a stalled one is aborted by the downloader's own watchdog
// long before it could look abandoned.
//
// A missing tree is not an error, and one unremovable entry does not abort the
// sweep.
func SweepStalePartialTrees(modelsPath string, olderThan time.Duration, keep string) (int, error) {
	partialRoot := filepath.Join(modelsPath, ".artifacts", ".partial")
	entries, err := os.ReadDir(partialRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		tree := filepath.Join(partialRoot, name)
		if tree == keep {
			continue
		}
		if _, _, ok := splitPartialDirName(name); !ok {
			continue
		}
		if !idleFor(tree, olderThan) {
			continue
		}
		if err := removePartialTree(partialRoot, tree); err != nil {
			xlog.Warn("failed to remove abandoned artifact partial", "partial", tree, "error", err)
			continue
		}
		removed++
		xlog.Info("removed abandoned artifact partial", "partial", tree)
	}
	return removed, nil
}
