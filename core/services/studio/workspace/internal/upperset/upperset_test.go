package upperset_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/internal/upperset"
)

var _ = Describe("upperset", func() {
	var blobs blob.Store

	BeforeEach(func() {
		var err error
		blobs, err = blob.NewFS(GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
	})

	view := workspace.View{Mounts: []workspace.Mount{
		{Path: "/home/ann", Mode: workspace.ModeWrite, NodeID: 1},
		{Path: "/shared", Mode: workspace.ModeRead, NodeID: 2},
		{Path: "/gone", Mode: workspace.ModeWrite, NodeID: 3},
	}}
	pts := map[int]string{0: "home", 1: "shared", 2: "gone"}

	It("maps the entries to the current paths of their mounts", func() {
		src := workspace.FixtureSource{
			workspace.FixtureDir("home"),
			workspace.FixtureOpaqueDir("home/docs", "user."),
			workspace.FixtureFile("home/docs/a.txt", "hello"),
			workspace.FixtureWhiteout("home/old.txt"),
			workspace.FixtureDir("shared"),
			workspace.FixtureFile("shared/x.txt", "no"),
			workspace.FixtureFile("elsewhere.txt", "no"),
			workspace.FixtureFile("gone/b.txt", "late"),
			workspace.FixtureSymlink("home/link"),
		}
		gone := map[int]bool{2: true}
		u, err := upperset.Read(context.Background(), view, pts, gone, src, blobs, 1<<20)
		Expect(err).ToNot(HaveOccurred())

		// The mount of ann was renamed after the view was taken.
		u.Place(view, map[int]string{0: "/home/ann2"}, gone)

		Expect(u.Dirs).To(Equal(map[string]bool{"/home/ann2": false, "/home/ann2/docs": true}))
		Expect(upperset.SortedKeys(u.Files)).To(Equal([]string{"/home/ann2/docs/a.txt"}))
		Expect(u.Files["/home/ann2/docs/a.txt"].Size).To(Equal(int64(5)))
		Expect(u.Files["/home/ann2/docs/a.txt"].Sum).ToNot(BeEmpty())
		Expect(u.Whiteouts).To(Equal([]string{"/home/ann2/old.txt"}))
		Expect(u.Where["/home/ann2/docs/a.txt"]).To(Equal(upperset.Loc{Mount: 0, Rel: []string{"docs", "a.txt"}}))
		Expect(u.Shown).To(HaveKey("/home/ann2/link"))
		Expect(u.Conflicts).To(ConsistOf(
			workspace.Conflict{Path: "/shared/x.txt", Reason: workspace.ReasonReadOnly},
			workspace.Conflict{Path: "/elsewhere.txt", Reason: workspace.ReasonOutsideView},
			workspace.Conflict{Path: "/home/ann2/link", Reason: workspace.ReasonUnsupported},
			workspace.Conflict{Path: "/gone/b.txt", Reason: workspace.ReasonParentDeleted},
		))
	})

	It("turns a file over the limit into a conflict", func() {
		src := workspace.FixtureSource{workspace.FixtureFile("home/big.bin", "0123456789")}
		u, err := upperset.Read(context.Background(), view, pts, nil, src, blobs, 4)
		Expect(err).ToNot(HaveOccurred())
		u.Place(view, map[int]string{0: "/home/ann"}, nil)
		Expect(u.Files).To(BeEmpty())
		Expect(u.Conflicts).To(Equal([]workspace.Conflict{{Path: "/home/ann/big.bin", Reason: workspace.ReasonTooLarge}}))
	})

	It("joins segments and orders paths by depth", func() {
		Expect(upperset.JoinRel("/", []string{"a", "b"})).To(Equal("/a/b"))
		Expect(upperset.JoinRel("/x", nil)).To(Equal("/x"))
		Expect(upperset.Depth("/a/b")).To(Equal(2))
		Expect(upperset.SortedKeys(map[string]int{"b": 1, "a": 2})).To(Equal([]string{"a", "b"}))
	})
})
