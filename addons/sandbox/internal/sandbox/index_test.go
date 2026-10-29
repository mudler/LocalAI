package sandbox_test

import (
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Upper index", func() {
	It("plans what to send from the last index", func() {
		first := []sandbox.IndexedEntry{dir("w", false), file("w/a.txt", "1"), file("w/b.txt", "1"), dir("w/d", true), file("w/d/x.txt", "1"), file("w/nokey.txt", "")}
		send, ix := sandbox.UpperIndex(nil).Plan(first)
		Expect(paths(send)).To(HaveLen(6))
		second := []sandbox.IndexedEntry{dir("w", false), file("w/a.txt", "1"), file("w/b.txt", "2"), dir("w/d", true), file("w/d/x.txt", "1"), file("w/nokey.txt", "")}
		send, ix2 := ix.Plan(second)
		Expect(paths(send)).To(Equal([]string{"d:w", "f:w/b.txt", "d:w/d", "f:w/d/x.txt", "f:w/nokey.txt"}))
		third := []sandbox.IndexedEntry{dir("w", false), file("w/b2.txt", "2")}
		send, _ = ix2.Plan(third)
		Expect(paths(send)).To(Equal([]string{"d:w", "f:w/b2.txt", "w:w/a.txt", "w:w/b.txt", "w:w/d", "w:w/d/x.txt", "w:w/nokey.txt"}))
	})

	It("sends a directory again when a file becomes one", func() {
		_, ix := sandbox.UpperIndex(nil).Plan([]sandbox.IndexedEntry{dir("w", false), file("w/a", "1")})
		send, _ := ix.Plan([]sandbox.IndexedEntry{dir("w", false), dir("w/a", false)})
		Expect(paths(send)).To(Equal([]string{"d:w", "d:w/a"}))
	})
})
