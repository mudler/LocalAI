package runner_test

import (
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the note of the paths a commit did not save", func() {
	// The server's own sandboxes name store paths; the add-on's name paths
	// relative to the user's files, as its mounts do.
	server := []workspace.Mount{{Path: "/users/alice", At: "home", Mode: workspace.ModeWrite}, {Path: "/shared/team", At: "shared-team", Mode: workspace.ModeRead}}
	addon := []workspace.Mount{{Path: "/", At: "home", Mode: workspace.ModeWrite}}

	It("names each path as the guest sees it", func() {
		Expect(runner.GuestPath("/users/alice/docs/a.txt", workspace.ReasonChanged, server)).To(Equal("/workspace/home/docs/a.txt"))
		Expect(runner.GuestPath("/shared/team/b.md", workspace.ReasonReadOnly, server)).To(Equal("/workspace/shared-team/b.md"))
		Expect(runner.GuestPath("/docs/a.txt", workspace.ReasonChanged, addon)).To(Equal("/workspace/home/docs/a.txt"))
		Expect(runner.GuestPath("/report.docx", workspace.ReasonOutsideView, addon)).To(Equal("/workspace/report.docx"))
	})

	It("says where the copy of a file changed meanwhile is, and why a file outside the home was not saved", func() {
		note := runner.ConflictNote([]workspace.Conflict{
			{Path: "/scripts/build.py", Reason: workspace.ReasonChanged, Kept: "/scripts/build (conflict 1).py"},
			{Path: "/report.docx", Reason: workspace.ReasonOutsideView},
		}, addon)
		Expect(note).To(ContainSubstring("[/workspace/home/scripts/build.py changed in the user's files while the command ran: their version stays at /workspace/home/scripts/build.py, and the command's version was saved as /workspace/home/scripts/build (conflict 1).py]"))
		Expect(note).To(ContainSubstring("[/workspace/report.docx was not saved: it is outside the user's files. Only what is under /workspace/home/ is kept; write it there]"))
		Expect(note).NotTo(ContainSubstring("kept as a copy"))
	})

	It("names at most ten paths and counts the rest", func() {
		var many []workspace.Conflict
		for range 13 {
			many = append(many, workspace.Conflict{Path: "/x", Reason: workspace.ReasonOutsideView})
		}
		Expect(runner.ConflictNote(many, addon)).To(ContainSubstring("[and 3 more paths were not saved as the command left them]"))
	})
})
