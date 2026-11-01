package workspace_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("Actor.Fenced", func() {
	alice := workspace.Actor{Principal: "user:alice", Groups: []string{"g"}, ChatID: "7"}
	r1 := workspace.FenceRoot{Node: 10, Mode: workspace.ModeWrite}
	r2 := workspace.FenceRoot{Node: 20, Mode: workspace.ModeRead}
	all := func(int64) bool { return true }

	It("leaves an actor without a fence unchanged", func() {
		_, ok := alice.Fence()
		Expect(ok).To(BeFalse())
		Expect(workspace.HasFence(alice)).To(BeFalse())
		Expect(workspace.FenceMode(alice, []int64{1}, all)).To(Equal(workspace.ModeWrite))
		Expect(workspace.CheckFence(alice)).To(Succeed())
	})

	It("keeps the principal, the groups and the chat", func() {
		a := alice.Fenced(r1)
		Expect(a.Principal).To(Equal(alice.Principal))
		Expect(a.Groups).To(Equal(alice.Groups))
		Expect(a.ChatID).To(Equal(alice.ChatID))
		_, ok := alice.Fence()
		Expect(ok).To(BeFalse(), "Fenced changed its receiver")
	})

	It("returns a copy of the roots", func() {
		roots := []workspace.FenceRoot{r1, r2}
		a := alice.Fenced(roots...)
		roots[0].Mode = workspace.ModeNone
		got, ok := a.Fence()
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal([]workspace.FenceRoot{r1, r2}), "the caller's slice reached the fence")
		got[1].Node = 99
		again, _ := a.Fence()
		Expect(again).To(Equal([]workspace.FenceRoot{r1, r2}), "the result of Fence reached the fence")
	})

	It("reaches nothing with no roots", func() {
		a := alice.Fenced()
		roots, ok := a.Fence()
		Expect(ok).To(BeTrue())
		Expect(roots).To(BeEmpty())
		Expect(workspace.FenceMode(a, []int64{10, 1}, all)).To(Equal(workspace.ModeNone))
	})

	It("gives each node the highest root above it and ignores a deleted root", func() {
		a := alice.Fenced(r1, r2)
		Expect(workspace.FenceMode(a, []int64{30, 10, 1}, all)).To(Equal(workspace.ModeWrite))
		Expect(workspace.FenceMode(a, []int64{20, 10, 1}, all)).To(Equal(workspace.ModeWrite), "20 is inside 10")
		Expect(workspace.FenceMode(a, []int64{20, 1}, all)).To(Equal(workspace.ModeRead))
		Expect(workspace.FenceMode(a, []int64{5, 1}, all)).To(Equal(workspace.ModeNone))
		Expect(workspace.FenceMode(a, []int64{30, 10, 1}, func(id int64) bool { return id != 10 })).To(Equal(workspace.ModeNone))
	})

	It("intersects when fenced twice and never widens", func() {
		a := alice.Fenced(r2).Fenced(workspace.FenceRoot{Node: 1, Mode: workspace.ModeWrite})
		Expect(workspace.FenceMode(a, []int64{20, 1}, all)).To(Equal(workspace.ModeRead))
		Expect(workspace.FenceMode(a, []int64{10, 1}, all)).To(Equal(workspace.ModeNone))
		roots, _ := a.Fence()
		Expect(roots).To(HaveLen(2))
	})

	It("compares fences", func() {
		a := alice.Fenced(r1, r2)
		b := a
		b.ChatID = ""
		Expect(a.SameFence(b)).To(BeTrue())
		Expect(a.SameFence(alice.Fenced(r1, r2))).To(BeTrue())
		Expect(a.SameFence(alice.Fenced(r2, r1))).To(BeFalse())
		Expect(a.SameFence(alice.Fenced(r1).Fenced(r2))).To(BeFalse())
		Expect(a.SameFence(alice)).To(BeFalse())
		Expect(alice.SameFence(workspace.Actor{Principal: "x"})).To(BeTrue())
		Expect(alice.Fenced().SameFence(alice)).To(BeFalse())
	})

	It("names its roots", func() {
		a := alice.Fenced(r1, r2).Fenced(r1)
		Expect(workspace.IsFenceRoot(a, 10)).To(BeTrue())
		Expect(workspace.IsFenceRoot(a, 20)).To(BeTrue())
		Expect(workspace.IsFenceRoot(a, 1)).To(BeFalse())
		Expect(workspace.FenceNodes(a)).To(Equal([]int64{10, 20}))
	})

	It("refuses an invalid fence", func() {
		roots := make([]workspace.FenceRoot, workspace.MaxFenceRoots+1)
		for i := range roots {
			roots[i] = r1
		}
		Expect(workspace.CheckFence(alice.Fenced(roots[:workspace.MaxFenceRoots]...))).To(Succeed())
		for name, a := range map[string]workspace.Actor{
			"too many roots": alice.Fenced(roots...),
			"no mode":        alice.Fenced(workspace.FenceRoot{Node: 1}),
			"bad mode":       alice.Fenced(workspace.FenceRoot{Node: 1, Mode: 7}),
			"no node":        alice.Fenced(workspace.FenceRoot{Mode: workspace.ModeRead}),
		} {
			Expect(workspace.CheckFence(a)).To(MatchError(workspace.ErrInvalid), name)
			Expect(workspace.FenceMode(a, []int64{1, 10}, all)).To(Equal(workspace.ModeNone), name)
		}
		deep := alice
		for range 9 {
			deep = deep.Fenced(r1)
		}
		Expect(workspace.CheckFence(deep)).To(MatchError(workspace.ErrInvalid), "too many layers")
	})
})
