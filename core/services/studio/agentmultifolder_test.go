package studio_test

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// multi saves an agent with the given folders.
func (r *accessRig) multi(name string, rows ...studio.AgentFolderWrite) (agents.Agent, error) {
	return r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{Name: name}, &rows)
}

func rw(path, mode string) studio.AgentFolderWrite {
	return studio.AgentFolderWrite{Path: path, Mode: mode}
}

var _ = Describe("an agent with several folders", func() {
	var r *accessRig
	BeforeEach(func() {
		r = newAccessRig()
		for _, d := range []string{"/Projects", "/Projects/Acme", "/Projects/Other", "/Notes", "/Secret"} {
			r.mkdir(d)
		}
		r.put("/Projects/Acme/a.txt", "acme")
		r.put("/Notes/n.txt", "notes")
		r.put("/Secret/s.txt", "secret")
		r.put("/Projects/Other/o.txt", "other")
	})

	It("keeps the list in order and refuses nine rows, a repeat and an empty path", func() {
		a, err := r.multi("Two", rw("/Projects/Acme", "write"), rw("/Notes", "read"))
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Folders).To(HaveLen(2))
		Expect(a.Folders[0].Mode).To(Equal(agents.FolderWrite))
		Expect(a.Folders[1].Mode).To(Equal(agents.FolderRead))
		rows := r.svc.AgentFolderRows(r.ctx, alice, []agents.Agent{a})[a.ID]
		Expect(rows).To(HaveLen(2))
		Expect(rows[1].Name).To(Equal("Notes"))

		var nine []studio.AgentFolderWrite
		for i := 0; i < agents.MaxFolders+1; i++ {
			d := "/Projects/D" + string(rune('A'+i))
			r.mkdir(d)
			nine = append(nine, rw(d, "read"))
		}
		_, err = r.multi("Nine", nine...)
		Expect(err).To(MatchError(studio.ErrAgentFolder))
		_, err = r.multi("Eight", nine[:agents.MaxFolders]...)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.multi("Twice", rw("/Notes", "read"), rw("/Notes", "write"))
		Expect(err).To(MatchError(studio.ErrAgentFolder))
		_, err = r.multi("Empty", rw("/Notes", "read"), rw("", "read"))
		Expect(err).To(MatchError(studio.ErrAgentFolder))
		_, err = r.multi("Mode", rw("/Notes", "all"))
		Expect(err).To(HaveOccurred())
	})

	It("lets a chat reach its folders and nothing next to them", func() {
		a, err := r.multi("Two", rw("/Projects/Acme", "write"), rw("/Notes", "read"))
		Expect(err).NotTo(HaveOccurred())
		x := r.access(r.chat(a.ID).ID)
		act := x.Actor()
		for _, p := range []string{"/Projects/Acme/a.txt", "/Notes/n.txt"} {
			_, err := r.ws.Stat(r.ctx, act, r.home+p)
			Expect(err).NotTo(HaveOccurred(), p)
		}
		for _, p := range []string{"/Secret/s.txt", "/Projects/Other/o.txt", "/Projects/Other", "/Projects"} {
			_, err := r.ws.Stat(r.ctx, act, r.home+p)
			Expect(err).To(HaveOccurred(), "%s is outside the folders", p)
		}
		_, err = r.ws.Put(r.ctx, act, r.home+"/Projects/Acme/new.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred(), "the write folder takes a write")
		_, err = r.ws.Put(r.ctx, act, r.home+"/Notes/new.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(HaveOccurred(), "the read folder refuses a write")
		_, err = r.ws.Put(r.ctx, act, r.home+"/Secret/new.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(HaveOccurred())

		v := x.View()
		var names []string
		for _, m := range v.Mounts {
			names = append(names, m.At)
		}
		Expect(names).To(ContainElements(session.GuestHome, "folder-Notes", session.GuestChat))
		Expect(x.Place().Primary()).To(Equal(r.home + "/Projects/Acme"))
		t, err := x.Place().Resolve("/workspace/folder-Notes/n.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Abs).To(Equal(r.home + "/Notes/n.txt"))
		Expect(t.Write).To(BeFalse())
		_, err = x.Place().Resolve("/workspace/folder-Notes/../../Secret/s.txt")
		Expect(err).To(HaveOccurred(), "a path that climbs out is refused")
	})

	It("caps the mode of a folder inside another folder by the outer mode", func() {
		a, err := r.multi("Nested", rw("/Projects", "read"), rw("/Projects/Acme", "write"))
		Expect(err).NotTo(HaveOccurred())
		rows := r.svc.AgentFolderRows(r.ctx, alice, []agents.Agent{a})[a.ID]
		Expect(rows[0].Effective).To(Equal("read"))
		Expect(rows[1].Mode).To(Equal("write"))
		Expect(rows[1].Effective).To(Equal("read"), "the outer mode is the inner mode")
		act := r.access(r.chat(a.ID).ID).Actor()
		_, err = r.ws.Put(r.ctx, act, r.home+"/Projects/Acme/new.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(HaveOccurred(), "the inner write is capped")
		_, err = r.ws.Stat(r.ctx, act, r.home+"/Projects/Other/o.txt")
		Expect(err).NotTo(HaveOccurred(), "the outer folder reads its children")
	})

	It("gives a read folder inside a write folder the write mode, and says so", func() {
		a, err := r.multi("Nested", rw("/Projects", "write"), rw("/Projects/Acme", "read"))
		Expect(err).NotTo(HaveOccurred())
		rows := r.svc.AgentFolderRows(r.ctx, alice, []agents.Agent{a})[a.ID]
		Expect(rows[1].Mode).To(Equal("read"))
		Expect(rows[1].Effective).To(Equal("write"), "the fence cannot hold a read only folder inside a write folder")
	})

	It("stops a chat that has a deleted or unchosen row and names the row", func() {
		a, err := r.multi("Two", rw("/Notes", "read"), rw("/Projects/Other", "write"))
		Expect(err).NotTo(HaveOccurred())
		err = r.ws.Delete(r.ctx, r.user, r.home+"/Projects/Other")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
		Expect(errors.Is(err, studio.ErrAgentFolderMissing)).To(BeTrue(), "%v", err)
		Expect(err.Error()).To(ContainSubstring("folder 2"))

		imp, err := r.svc.ImportAgentFolders(r.ctx, alice, agents.Agent{Name: "Imported"}, []studio.AgentFolderWrite{rw("/Notes", "read"), rw("/Nowhere", "write")})
		Expect(err).NotTo(HaveOccurred())
		Expect(imp.Folders).To(HaveLen(2))
		Expect(imp.Folders[0].Node).NotTo(BeZero())
		Expect(imp.Folders[1].Node).To(BeZero(), "a folder not found is a row that is not chosen")
		_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: imp.ID})
		Expect(errors.Is(err, studio.ErrAgentFolderMissing)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("folder 2"))
	})

	It("does not fall back to the whole home when one row is lost", func() {
		a, err := r.multi("Two", rw("/Notes", "read"), rw("/Projects/Other", "write"))
		Expect(err).NotTo(HaveOccurred())
		rec := r.chat(a.ID)
		err = r.ws.Delete(r.ctx, r.user, r.home+"/Projects/Other")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.ChatAccess(r.ctx, alice, rec.ID)
		Expect(errors.Is(err, studio.ErrAgentFolderMissing)).To(BeTrue(), "%v", err)
	})
})
