//go:build linux

package gvisor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// bigLimits are limits that no test layer reaches.
var bigLimits = readLimits{stage: 1 << 40}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// summary lists "path kind" for the entries, sorted, with the content of
// files read from their Open.
func summary(t *testing.T, src stagedSource) []string {
	t.Helper()
	var out []string
	err := src.Walk(context.Background(), func(e workspace.UpperEntry) error {
		line := e.Path + " " + map[workspace.EntryKind]string{
			workspace.EntryFile: "file", workspace.EntryDir: "dir", workspace.EntryWhiteout: "whiteout", workspace.EntrySymlink: "symlink",
		}[e.Kind]
		if e.Kind == workspace.EntryFile {
			r, err := e.Open()
			if err != nil {
				return err
			}
			b, _ := io.ReadAll(r)
			r.Close()
			line += " " + string(b)
		}
		out = append(out, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestReadUpperSendsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	root, stage := filepath.Join(t.TempDir(), "data"), t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "a1")
	write(t, filepath.Join(root, "d", "b.txt"), "b1")

	src, next, err := readUpper(ctx, root, stage, sandbox.UpperIndex{}, bigLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(summary(t, src), "|"); got != "a.txt file a1|d dir|d/b.txt file b1" {
		t.Fatalf("first read: %s", got)
	}
	// The staged copies do not follow later writes to the live files.
	write(t, filepath.Join(root, "a.txt"), "a2-longer")
	if got := strings.Join(summary(t, src), "|"); !strings.Contains(got, "a.txt file a1") {
		t.Fatalf("staged content changed: %s", got)
	}

	// Nothing changed since the index was made: only the folder is sent again
	// (a folder has no key). Then the edit of a.txt is sent, the rest is not.
	src, next2, err := readUpper(ctx, root, t.TempDir(), next, bigLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(summary(t, src), "|"); got != "a.txt file a2-longer|d dir" {
		t.Fatalf("second read: %s", got)
	}
	// A file that an earlier checkpoint committed and the guest deleted
	// since: overlayfs leaves no whiteout for it, the index does.
	if err := os.Remove(filepath.Join(root, "d", "b.txt")); err != nil {
		t.Fatal(err)
	}
	src, _, err = readUpper(ctx, root, t.TempDir(), next2, bigLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(summary(t, src), "|"); got != "d dir|d/b.txt whiteout" {
		t.Fatalf("third read: %s", got)
	}
}

func TestReadUpperOfAMissingFolderIsEmpty(t *testing.T) {
	src, next, err := readUpper(context.Background(), filepath.Join(t.TempDir(), "none"), t.TempDir(), sandbox.UpperIndex{}, bigLimits)
	if err != nil || len(src) != 0 || len(next) != 0 {
		t.Fatalf("%v %v %v", src, next, err)
	}
}

func TestStatKeyChangesWithTheFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	write(t, p, "one")
	st1, _ := os.Lstat(p)
	k1 := statKey(st1)
	if k1 == "" {
		t.Fatal("empty key")
	}
	// The same content with a different modification time is a different key,
	// whatever the clock granularity is.
	if err := os.Chtimes(p, time.Unix(1_000_000_000, 0), time.Unix(1_000_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Lstat(p)
	k2 := statKey(st2)
	if k1 == k2 {
		t.Fatalf("key did not change with the mtime: %q", k1)
	}
	// A different size is a different key.
	write(t, p, "three")
	st3, _ := os.Lstat(p)
	if k3 := statKey(st3); k3 == k2 || k3 == k1 {
		t.Fatalf("key did not change with the size: %q %q %q", k1, k2, k3)
	}
	// An unchanged file keeps its key.
	st4, _ := os.Lstat(p)
	if statKey(st4) != statKey(st3) {
		t.Fatal("key of an unchanged file changed")
	}
}

func TestDirUsageCountsBlocks(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a"), strings.Repeat("x", 100000))
	if n, capped := dirUsage(dir, 100); n < 100000 || capped {
		t.Fatalf("usage = %d, capped %v", n, capped)
	}
	if n, capped := dirUsage(filepath.Join(dir, "missing"), 100); n != 0 || capped {
		t.Fatalf("usage of a missing folder = %d, capped %v", n, capped)
	}
}

func TestDirUsageStopsAtTheEntryCap(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d"} {
		write(t, filepath.Join(dir, n), "")
	}
	// The root and four files are five entries.
	if _, capped := dirUsage(dir, 5); capped {
		t.Fatal("five entries are within a cap of five")
	}
	if _, capped := dirUsage(dir, 4); !capped {
		t.Fatal("five entries are over a cap of four")
	}
}

func TestMeasureDiskSetsTheFlagAtTheEntryCapAndDropsStaleReadings(t *testing.T) {
	dir := t.TempDir()
	s := &sbx{dir: dir}
	upper := filepath.Join(dir, "upper")
	write(t, filepath.Join(upper, "a"), "")
	in := newInst(1)
	s.measureDisk(in, 1<<30)
	if s.diskFull.Load() {
		t.Fatal("a small layer is full")
	}
	s.measureDisk(in, -1) // over the limit
	if !s.diskFull.Load() {
		t.Fatal("a layer over the limit is not full")
	}
	// A refresh stops the old instance and clears the flag. A reading of the
	// old instance that finishes later must not set it again.
	s.diskFull.Store(false)
	close(in.quit)
	s.measureDisk(in, -1)
	if s.diskFull.Load() {
		t.Fatal("a reading of a stopped instance set the flag")
	}
}

func TestTheUpperCapsAreTheOnesOfChv(t *testing.T) {
	if maxUpperDepth != 512 || maxUpperEntries != 1000000 {
		t.Fatalf("caps: %d levels, %d entries", maxUpperDepth, maxUpperEntries)
	}
	if got := (readLimits{}).withDefaults(); got.depth != 512 || got.entries != 1000000 {
		t.Fatalf("defaults: %+v", got)
	}
}

func TestReadUpperFailsOverTheEntryCapAndNamesIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	for _, n := range []string{"a", "b", "c", "d"} {
		write(t, filepath.Join(root, n), "x")
	}
	lim := readLimits{entries: 4, stage: 1 << 20}
	if _, _, err := readUpper(context.Background(), root, t.TempDir(), sandbox.UpperIndex{}, lim); err != nil {
		t.Fatalf("four entries within a cap of four: %v", err)
	}
	stage := t.TempDir()
	lim.entries = 3
	_, _, err := readUpper(context.Background(), root, stage, sandbox.UpperIndex{}, lim)
	if err == nil || !strings.Contains(err.Error(), "more than 3 entries") {
		t.Fatalf("err = %v", err)
	}
	if ents, _ := os.ReadDir(stage); len(ents) != 0 {
		t.Fatalf("a failed read staged %d files", len(ents))
	}
}

func TestReadUpperFailsOverTheDepthCapAndNamesIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(root, "a", "b", "c", "f"), "x")
	lim := readLimits{depth: 4, stage: 1 << 20}
	if _, _, err := readUpper(context.Background(), root, t.TempDir(), sandbox.UpperIndex{}, lim); err != nil {
		t.Fatalf("four levels within a cap of four: %v", err)
	}
	lim.depth = 3
	_, _, err := readUpper(context.Background(), root, t.TempDir(), sandbox.UpperIndex{}, lim)
	if err == nil || !strings.Contains(err.Error(), "deeper than 3 levels") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadUpperFailsOverTheDefaultDepthCap(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	deep := root
	for range maxUpperDepth + 1 {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Skipf("cannot make %d levels: %v", maxUpperDepth+1, err)
	}
	_, _, err := readUpper(context.Background(), root, t.TempDir(), sandbox.UpperIndex{}, readLimits{stage: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "deeper than 512 levels") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadUpperFailsOverTheStageLimit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(root, "a"), strings.Repeat("x", 600))
	write(t, filepath.Join(root, "b"), strings.Repeat("y", 600))
	stage := t.TempDir()
	_, _, err := readUpper(context.Background(), root, stage, sandbox.UpperIndex{}, readLimits{stage: 1000})
	if err == nil || !strings.Contains(err.Error(), "stage limit of 1000 bytes") {
		t.Fatalf("err = %v", err)
	}
	if ents, _ := os.ReadDir(stage); len(ents) != 0 {
		t.Fatalf("a failed read staged %d files", len(ents))
	}
	if _, _, err := readUpper(context.Background(), root, t.TempDir(), sandbox.UpperIndex{}, readLimits{stage: 1200}); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	// Files that are not sent (unchanged) do not count.
	_, next, err := readUpper(context.Background(), root, t.TempDir(), sandbox.UpperIndex{}, readLimits{stage: 1200})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readUpper(context.Background(), root, t.TempDir(), next, readLimits{stage: 1}); err != nil {
		t.Fatalf("unchanged files counted against the limit: %v", err)
	}
}
