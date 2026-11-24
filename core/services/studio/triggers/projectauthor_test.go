package triggers_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

// A grantee may write into the files folder of a project that its owner
// shares. The change journal records the author of every change set, so the
// file triggers of the owner start only for what the owner or the owner's own
// chats wrote.
var _ = Describe("File triggers in a shared project", func() {
	const (
		projFiles = "/users/alice/Projects/acme/files"
		alice     = "alice"
		bob       = "bob"
	)
	// shared makes a rig whose trigger belongs to alice and watches the files
	// folder of her project, with bob holding a write grant on the project.
	shared := func() *rig {
		r := newRig()
		r.write(projFiles+"/seed.txt", "x")
		Expect(r.ws.Grant(ctx, wstest.AdminActor, alice, "/users/alice", workspace.ModeWrite)).To(Succeed())
		Expect(r.ws.Grant(ctx, wstest.AdminActor, bob, "/users/alice/Projects/acme", workspace.ModeWrite)).To(Succeed())
		tr := fileTrigger("t1", projFiles)
		tr.Owner = alice
		r.put(tr)
		r.sync()
		return r
	}
	person := func(p string) workspace.Actor { return workspace.Actor{Principal: p} }
	chatOf := func(p string) workspace.Actor { return workspace.Actor{Principal: p, ChatID: "chat-of-" + p} }

	It("ignores a file that a grantee writes", func() {
		r := shared()
		r.writeAs(person(bob), projFiles+"/theirs.txt", "from bob")
		r.sync()
		r.after(10 * time.Minute)
		Expect(r.runs("t1")).To(BeEmpty())
	})

	It("ignores a file that a chat of a grantee writes", func() {
		r := shared()
		r.writeAs(chatOf(bob), projFiles+"/theirs.txt", "from bob's agent")
		r.sync()
		r.after(10 * time.Minute)
		Expect(r.runs("t1")).To(BeEmpty())
	})

	It("starts a run for a file that the owner writes", func() {
		r := shared()
		r.writeAs(person(alice), projFiles+"/mine.txt", "from alice")
		r.sync()
		r.after(10 * time.Minute)
		runs := r.runs("t1")
		Expect(runs).To(HaveLen(1))
		Expect(runs[0].File).To(Equal(projFiles + "/mine.txt"))
	})

	It("starts a run for a file that a chat of the owner writes", func() {
		r := shared()
		r.writeAs(chatOf(alice), projFiles+"/agent.txt", "from alice's agent")
		r.sync()
		r.after(10 * time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1))
	})

	It("does not run on a file of the owner that a grantee changed while it waited", func() {
		r := shared()
		r.writeAs(person(alice), projFiles+"/mine.txt", "from alice")
		r.sync()
		r.writeAs(person(bob), projFiles+"/mine.txt", "changed by bob")
		r.sync()
		r.after(10 * time.Minute)
		Expect(r.runs("t1")).To(BeEmpty())
	})

	It("ignores a grantee's move of the owner's waiting file inside the project", func() {
		r := shared()
		r.writeAs(person(alice), projFiles+"/mine.txt", "from alice")
		r.sync()
		_, err := r.ws.Move(ctx, person(bob), projFiles+"/mine.txt", projFiles+"/renamed.txt")
		Expect(err).NotTo(HaveOccurred())
		r.sync()
		r.after(10 * time.Minute)
		Expect(r.runs("t1")).To(BeEmpty())
	})

	// A trigger on a folder above the project sees the project too: the
	// rule follows the path of each change, not the folder of the trigger.
	DescribeTable("a trigger on a folder that holds the project",
		func(top string) {
			r := newRig()
			r.write(projFiles+"/seed.txt", "x")
			r.write("/users/alice/Team/seed.txt", "x")
			Expect(r.ws.Grant(ctx, wstest.AdminActor, alice, "/users/alice", workspace.ModeWrite)).To(Succeed())
			Expect(r.ws.Grant(ctx, wstest.AdminActor, bob, "/users/alice/Projects/acme", workspace.ModeWrite)).To(Succeed())
			Expect(r.ws.Grant(ctx, wstest.AdminActor, bob, "/users/alice/Team", workspace.ModeWrite)).To(Succeed())
			tr := fileTrigger("t1", top)
			tr.Owner = alice
			r.put(tr)
			r.sync()

			r.writeAs(person(bob), projFiles+"/theirs.txt", "from bob")
			r.sync()
			r.after(10 * time.Minute)
			Expect(r.runs("t1")).To(BeEmpty(), "a grantee write into the project")

			r.writeAs(person(alice), projFiles+"/mine.txt", "from alice")
			r.sync()
			r.after(10 * time.Minute)
			Expect(r.runs("t1")).To(HaveLen(1), "the owner's write into the project")
			Expect(r.latest("t1").File).To(Equal(projFiles + "/mine.txt"))

			if top == "/users/alice" { // Team is inside the home, outside any project
				r.writeAs(person(bob), "/users/alice/Team/drop.txt", "from bob")
				r.sync()
				r.after(10 * time.Minute)
				Expect(r.runs("t1")).To(HaveLen(2), "a grantee write outside any project")
				Expect(r.latest("t1").File).To(Equal("/users/alice/Team/drop.txt"))
			}
		},
		Entry("the Projects folder", "/users/alice/Projects"),
		Entry("the home of the owner", "/users/alice"),
	)

	It("keeps starting runs for other authors in a folder outside any project", func() {
		r := armed()
		r.write(folder+"/a.txt", "by the administrator")
		r.sync()
		r.after(10 * time.Minute)
		Expect(r.runs("t1")).To(HaveLen(1))
	})
})
