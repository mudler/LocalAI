package chv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The test image of testUpper is 16 MiB, so its default stage budget is that.
const testImageBytes = 16 << 20

// addSparse adds data/w/sparse.db, a file of one block whose apparent size is
// size bytes.
func addSparse(t *testing.T, img string, size int64) {
	t.Helper()
	src := filepath.Join(filepath.Dir(img), "src", "new.txt")
	debugfsWrite(t, img, "write "+src+" data/w/sparse.db", fmt.Sprintf("sif data/w/sparse.db size %d", size))
}

func hasPath(src workspace.FixtureSource, p string) bool {
	for _, e := range src {
		if e.Path == p {
			return true
		}
	}
	return false
}

// An old sparse file that is larger than the budget but unchanged must not
// block later reads: it is not sent, so it does not count.
func TestStageUnchangedSparseFileDoesNotBlock(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img, r := testUpper(t)
	addSparse(t, img, 40<<20)
	// The first read stages the file, under a budget that fits it.
	r.lim.stage = 64 << 20
	src, ix, err := r.read(readCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPath(src, "w/sparse.db") {
		t.Fatal("the first read did not send the sparse file")
	}
	// The next read has the default budget, the image size. The file is
	// unchanged, so the index does not send it.
	r.lim.stage = 0
	src, _, err = r.read(readCtx(t), ix)
	if err != nil {
		t.Fatalf("an unchanged sparse file blocked the read: %v", err)
	}
	if hasPath(src, "w/sparse.db") {
		t.Fatal("an unchanged file was sent again")
	}
}

// A file that the index sends and that is larger than the budget fails the
// read before anything is staged, and the error names the path.
func TestStageSendSparseOverBudget(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img, r := testUpper(t)
	addSparse(t, img, 40<<20)
	_, _, err := r.read(readCtx(t), nil)
	want := fmt.Sprintf(`staging "w/sparse.db" (%d bytes) goes over the stage limit of %d bytes`, 40<<20, testImageBytes)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("a sparse file over the budget: %v", err)
	}
	if entries, _ := os.ReadDir(r.stage); len(entries) != 0 {
		t.Fatalf("%d files were staged before the budget check failed", len(entries))
	}
}

// Files under the budget stage. Each inode counts once, so a second name for
// the same inode does not count twice: new.txt (4 bytes), edit.txt (7) and
// x.txt (2) add up to 13 bytes, with hard.txt as a second name for new.txt.
func TestStageUnderBudget(t *testing.T) {
	needTools(t, "mke2fs", "debugfs")
	img, r := testUpper(t)
	debugfsWrite(t, img, "link data/w/new.txt data/w/hard.txt")
	r.lim.stage = 13
	if _, _, err := r.read(readCtx(t), nil); err != nil {
		t.Fatalf("13 bytes of files in a 13-byte budget: %v", err)
	}
	r.lim.stage = 12
	_, _, err := r.read(readCtx(t), nil)
	if err == nil || !strings.Contains(err.Error(), "stage limit of 12 bytes") {
		t.Fatalf("12 bytes of budget for 13 bytes of files: %v", err)
	}
}
