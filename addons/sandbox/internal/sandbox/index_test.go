package sandbox_test

import (
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func file(p, key string) sandbox.IndexedEntry {
	return sandbox.IndexedEntry{UpperEntry: workspace.FixtureFile(p, "x"), Key: key}
}

func dir(p string, opaque bool) sandbox.IndexedEntry {
	e := sandbox.IndexedEntry{UpperEntry: workspace.FixtureDir(p)}
	if opaque {
		e.UpperEntry = workspace.FixtureOpaqueDir(p, "trusted.")
	}
	return e
}

func paths(es []workspace.UpperEntry) []string {
	var out []string
	for _, e := range es {
		k := map[workspace.EntryKind]string{workspace.EntryFile: "f", workspace.EntryDir: "d", workspace.EntryWhiteout: "w"}[e.Kind]
		out = append(out, k+":"+e.Path)
	}
	return out
}

func TestUpperIndexPlan(t *testing.T) {
	first := []sandbox.IndexedEntry{dir("w", false), file("w/a.txt", "1"), file("w/b.txt", "1"), dir("w/d", true), file("w/d/x.txt", "1"), file("w/nokey.txt", "")}
	send, ix := sandbox.UpperIndex(nil).Plan(first)
	if got := paths(send); len(got) != 6 {
		t.Fatalf("first plan sends %v", got)
	}
	second := []sandbox.IndexedEntry{dir("w", false), file("w/a.txt", "1"), file("w/b.txt", "2"), dir("w/d", true), file("w/d/x.txt", "1"), file("w/nokey.txt", "")}
	send, ix2 := ix.Plan(second)
	want := []string{"d:w", "f:w/b.txt", "d:w/d", "f:w/d/x.txt", "f:w/nokey.txt"}
	if got := paths(send); !reflect.DeepEqual(got, want) {
		t.Fatalf("second plan = %v, want %v", got, want)
	}
	third := []sandbox.IndexedEntry{dir("w", false), file("w/b2.txt", "2")}
	send, _ = ix2.Plan(third)
	want = []string{"d:w", "f:w/b2.txt", "w:w/a.txt", "w:w/b.txt", "w:w/d", "w:w/d/x.txt", "w:w/nokey.txt"}
	if got := paths(send); !reflect.DeepEqual(got, want) {
		t.Fatalf("third plan = %v, want %v", got, want)
	}
}

func TestUpperIndexKindChange(t *testing.T) {
	_, ix := sandbox.UpperIndex(nil).Plan([]sandbox.IndexedEntry{dir("w", false), file("w/a", "1")})
	send, _ := ix.Plan([]sandbox.IndexedEntry{dir("w", false), dir("w/a", false)})
	if got := paths(send); !reflect.DeepEqual(got, []string{"d:w", "d:w/a"}) {
		t.Fatalf("plan = %v", got)
	}
}
