package session_test

import (
	"math/rand/v2"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

const (
	placeHome   = "/users/alice"
	placeFolder = "/users/alice/Projects/Acme"
	placeChat   = "/users/alice/Chats/2026-11-01 Plan"
	placeShared = "/shared/team"
)

// scopedPlace is the place of a chat whose agent works in Projects/Acme, read
// only, with the chat folder and one shared folder.
func scopedPlace() session.Place {
	a := workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(
		workspace.FenceRoot{Node: 10, Mode: workspace.ModeRead},
		workspace.FenceRoot{Node: 11, Mode: workspace.ModeWrite},
	)
	return session.NewPlace(workspace.View{Actor: a, Mounts: []workspace.Mount{
		{Path: placeFolder, At: session.GuestHome, Mode: workspace.ModeRead, NodeID: 10},
		{Path: placeChat, At: session.GuestChat, Mode: workspace.ModeWrite, NodeID: 11},
		{Path: placeShared, At: "shared-team", Mode: workspace.ModeRead, NodeID: 12},
	}})
}

func unscopedPlace() session.Place {
	return session.NewPlace(workspace.View{Actor: workspace.Actor{Principal: "alice", ChatID: "c1"}, Mounts: []workspace.Mount{
		{Path: placeHome, At: session.GuestHome, Mode: workspace.ModeWrite},
		{Path: placeShared, At: "shared-team", Mode: workspace.ModeWrite},
	}})
}

// insideOneMount reports whether abs is a mount of the place or below one.
func insideOneMount(abs string) bool {
	for _, root := range []string{placeFolder, placeChat, placeShared, "/skills", "/builtin-skills", "/addon-skills"} {
		if abs == root || strings.HasPrefix(abs, root+"/") {
			return true
		}
	}
	return false
}

var _ = Describe("Place", func() {
	It("maps every home spelling to the primary mount", func() {
		p := scopedPlace()
		for _, s := range []string{"/a.txt", "a.txt", "home/a.txt", "/workspace/home/a.txt", "workspace/home/a.txt", " /a.txt "} {
			t, err := p.Resolve(s)
			Expect(err).NotTo(HaveOccurred(), s)
			Expect(t.Abs).To(Equal(placeFolder+"/a.txt"), s)
			Expect(t.Root).To(Equal(placeFolder), s)
			Expect(t.Write).To(BeFalse(), s)
			Expect(t.Guest(t.Abs)).To(Equal("/workspace/home/a.txt"), s)
			Expect(t.Show(t.Abs)).To(Equal("/Projects/Acme/a.txt"), s)
		}
		for _, s := range []string{"", "/", "home", "/workspace", "/workspace/", "/workspace/home", "workspace"} {
			t, err := p.Resolve(s)
			Expect(err).NotTo(HaveOccurred(), s)
			Expect(t.Abs).To(Equal(placeFolder), s)
		}
		Expect(p.Primary()).To(Equal(placeFolder))
		Expect(p.Scoped()).To(BeTrue())
		Expect(unscopedPlace().Scoped()).To(BeFalse())
		t, err := unscopedPlace().Resolve("/workspace/home/a.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Abs).To(Equal(placeHome + "/a.txt"))
		Expect(t.Write).To(BeTrue())
		Expect(t.Show(t.Abs)).To(Equal("/a.txt"))
	})

	It("maps /workspace/chat to the chat folder", func() {
		p := scopedPlace()
		t, err := p.Resolve("/workspace/chat/work/run.py")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Abs).To(Equal(placeChat + "/work/run.py"))
		Expect(t.Root).To(Equal(placeChat))
		Expect(t.Write).To(BeTrue())
		Expect(t.Guest(t.Abs)).To(Equal("/workspace/chat/work/run.py"))
		Expect(p.Chat()).To(Equal(placeChat))
		g, ok := p.Guest(placeChat + "/a.pdf")
		Expect(ok).To(BeTrue())
		Expect(g).To(Equal("/workspace/chat/a.pdf"))
		g, ok = p.Guest(placeShared + "/x.md")
		Expect(ok).To(BeTrue())
		Expect(g).To(Equal("/workspace/shared-team/x.md"))
		_, ok = p.Guest(placeHome + "/Other/x.pdf")
		Expect(ok).To(BeFalse())
		_, ok = p.Guest("/users/alice/Projects/Acme2/x")
		Expect(ok).To(BeFalse())
		// An unscoped chat has no chat mount: chat/ is a folder of the home.
		_, err = unscopedPlace().Resolve("/workspace/chat/x")
		Expect(err).To(HaveOccurred())
	})

	It("keeps the home spelling of the chat folder that attachment lists use", func() {
		p := scopedPlace()
		t, err := p.Resolve("/workspace/home/Chats/2026-11-01 Plan/invoice.pdf")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Abs).To(Equal(placeChat + "/invoice.pdf"))
		Expect(t.At).To(Equal(session.GuestChat))
		Expect(t.Write).To(BeTrue())
		// Another chat's folder is a path in the agent's folder, as any name.
		t, err = p.Resolve("/workspace/home/Chats/2026-10-01 Other/x.pdf")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Abs).To(Equal(placeFolder + "/Chats/2026-10-01 Other/x.pdf"))
	})

	It("refuses parents, NUL and control characters wherever they point", func() {
		for _, p := range []session.Place{scopedPlace(), unscopedPlace()} {
			for _, s := range []string{
				"../x", "/../x", "..", "/workspace/home/../x", "/workspace/chat/../../x",
				"/workspace/shared-team/../home/x", "/workspace/home//../x", "home/../../x",
				"a/b/../../../users/bob", "/workspace/../users/bob",
				"a\x00b", "/workspace/home/\x00", "a\nb", "a\tb", "a\x7fb", "\x1b[0m",
				"bad\xffutf8",
			} {
				_, err := p.Resolve(s)
				Expect(err).To(MatchError(workspace.ErrBadName), "%q", s)
			}
		}
	})

	It("takes encoded and look-alike separators as literal names", func() {
		p := scopedPlace()
		for _, s := range []string{"..%2fx", "%2e%2e/x", `..\x`, `\..\x`, "․․/x", "a／b", "․․"} {
			t, err := p.Resolve(s)
			Expect(err).NotTo(HaveOccurred(), "%q", s)
			Expect(strings.HasPrefix(t.Abs, placeFolder+"/")).To(BeTrue(), "%q gave %s", s, t.Abs)
		}
		t, err := p.Resolve("..%2fx")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Abs).To(Equal(placeFolder + "/..%2fx"))
	})

	It("refuses a /workspace path that is no mount, case-sensitively", func() {
		p := scopedPlace()
		for _, s := range []string{"/workspace/Home/x", "/workspace/HOME", "/workspace/Chat/x", "/workspace/newsapp", "/workspace/shared-other/x", "/workspace/shared-Team/x", "workspace/users/alice"} {
			_, err := p.Resolve(s)
			Expect(err).To(MatchError(ContainSubstring("is not a place of the user's files")), s)
		}
		_, err := p.Resolve("/workspace/newsapp")
		Expect(err.Error()).NotTo(ContainSubstring("Page"), "a scoped chat gets no page hint")
		_, err = unscopedPlace().Resolve("/workspace/newsapp")
		Expect(err.Error()).To(ContainSubstring("Page"))
		// Other spellings of a home or another user stay inside the folder.
		for _, s := range []string{"/Workspace/Home/x", "/users/alice/x", "/users/bob/x"} {
			t, err := p.Resolve(s)
			Expect(err).NotTo(HaveOccurred(), s)
			Expect(strings.HasPrefix(t.Abs, placeFolder+"/")).To(BeTrue(), s)
		}
		_, err = p.Resolve("//users/bob/x")
		Expect(err).To(HaveOccurred())
	})

	It("maps the skill layers read only", func() {
		t, err := scopedPlace().Resolve("/workspace/shared-skills/pdf/SKILL.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Abs).To(Equal("/skills/pdf/SKILL.md"))
		Expect(t.Write).To(BeFalse())
		Expect(t.Layer).To(BeTrue())
	})

	It("never resolves outside its mounts", func() {
		r := rand.New(rand.NewPCG(1, 2))
		parts := []string{"..", ".", "", "workspace", "home", "chat", "shared-team", "shared-skills", "users", "alice", "bob",
			"Projects", "Acme", "Chats", "x", "%2e%2e", "․․", `..\`, "／", "HOME", "\x00", " ", "~", "*"}
		p := scopedPlace()
		for range 5000 {
			n := 1 + r.IntN(6)
			segs := make([]string, n)
			for i := range segs {
				segs[i] = parts[r.IntN(len(parts))]
			}
			s := strings.Join(segs, "/")
			if r.IntN(2) == 0 {
				s = "/" + s
			}
			t, err := p.Resolve(s)
			if err != nil {
				continue
			}
			Expect(insideOneMount(t.Abs)).To(BeTrue(), "%q resolved to %s", s, t.Abs)
			Expect(insideOneMount(t.Root)).To(BeTrue())
			Expect(t.Abs == t.Root || strings.HasPrefix(t.Abs, t.Root+"/")).To(BeTrue(), "%q", s)
		}
	})

	It("is empty for a view without mounts", func() {
		p := session.NewPlace(workspace.View{Actor: workspace.Actor{Principal: "alice"}})
		_, err := p.Resolve("/a.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound))
		Expect(p.Primary()).To(BeEmpty())
	})
})

var _ = Describe("Access", func() {
	It("keeps the fence on its actor and drops it for the owner", func() {
		a := workspace.Actor{Principal: "alice", ChatID: "c1", Groups: []string{"everyone"}}.Fenced(workspace.FenceRoot{Node: 10, Mode: workspace.ModeRead})
		x, err := session.NewAccess(a, workspace.View{Mounts: []workspace.Mount{{Path: placeFolder, At: session.GuestHome, Mode: workspace.ModeRead}}})
		Expect(err).NotTo(HaveOccurred())
		_, fenced := x.Actor().Fence()
		Expect(fenced).To(BeTrue())
		Expect(x.Actor().ChatID).To(Equal("c1"))
		_, fenced = x.Owner().Fence()
		Expect(fenced).To(BeFalse())
		Expect(x.Owner().ChatID).To(BeEmpty())
		Expect(x.Owner().Groups).To(Equal([]string{"everyone"}))
		Expect(x.Place().Primary()).To(Equal(placeFolder))
		Expect(x.Place().Scoped()).To(BeTrue())
		Expect(x.View().Actor.Principal).To(Equal("alice"))
	})

	It("refuses an actor without a valid principal", func() {
		for _, p := range []string{"", "..", "a/b", workspace.StudioAdmin} {
			_, err := session.NewAccess(workspace.Actor{Principal: p}, workspace.View{})
			Expect(err).To(HaveOccurred(), p)
		}
	})
})
