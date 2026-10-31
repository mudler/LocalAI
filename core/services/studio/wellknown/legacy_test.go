package wellknown_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

// noMoves is a store that refuses every move, as a store error would.
type noMoves struct{ workspace.Store }

var errNoMove = errors.New("the store refused the move")

func (noMoves) Move(context.Context, workspace.Actor, string, string) (workspace.Node, error) {
	return workspace.Node{}, errNoMove
}

func (n noMoves) Begin(ctx context.Context, a workspace.Actor, title string) (workspace.Tx, error) {
	tx, err := n.Store.Begin(ctx, a, title)
	if err != nil {
		return nil, err
	}
	return noMoveTx{tx}, nil
}

type noMoveTx struct{ workspace.Tx }

func (noMoveTx) Move(context.Context, string, string) (workspace.Node, error) {
	return workspace.Node{}, errNoMove
}

func exists(f *files.Service, who, rel string) bool {
	abs, err := f.Abs(who, rel)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.Store().Stat(context.Background(), f.Actor(who), abs)
	if errors.Is(err, workspace.ErrNotFound) {
		return false
	}
	Expect(err).NotTo(HaveOccurred())
	return true
}

var _ = Describe("the settings files in LocalAI/", func() {
	ctx := context.Background()

	It("keeps every settings file in LocalAI/, and widget data where it is", func() {
		for _, n := range []wellknown.Name{wellknown.Persona, wellknown.User, wellknown.Shortcuts, wellknown.Home, wellknown.UI} {
			Expect(wellknown.File(n)).To(Equal("/LocalAI/" + string(n) + ".json"))
		}
		Expect(wellknown.File(wellknown.WidgetData)).To(BeEmpty())
		Expect(wellknown.Dir).To(Equal("/LocalAI"))
	})

	It("writes a new file into LocalAI/ and nothing at the top of the home", func() {
		s, f := newService()
		doc, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"theme": "light"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Path).To(Equal("/LocalAI/ui.json"))
		Expect(exists(f, alice, "/LocalAI/ui.json")).To(BeTrue())
		Expect(exists(f, alice, "/ui.json")).To(BeFalse())
	})

	It("moves a file from the top of the home on first access, and keeps its versions", func() {
		s, f := newService()
		raw(f, alice, "/persona.json", `{"name":"Ada"}`)
		raw(f, alice, "/persona.json", `{"name":"Bea"}`)
		doc, err := s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Path).To(Equal("/LocalAI/persona.json"))
		Expect(doc.Data).To(HaveKeyWithValue("name", "Bea"))
		Expect(exists(f, alice, "/persona.json")).To(BeFalse())

		vs, err := s.Versions(ctx, alice, wellknown.Persona, "", 10)
		Expect(err).NotTo(HaveOccurred())
		var names []any
		for _, v := range vs {
			names = append(names, v.Data["name"])
		}
		Expect(names).To(Equal([]any{"Bea", "Ada"}))
		// The version from before the move can be written back.
		doc, err = s.RevertTo(ctx, alice, wellknown.Persona, "", vs[1].Rev)
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Data).To(HaveKeyWithValue("name", "Ada"))
	})

	It("names the move in the history, and Undo of it brings the old file back", func() {
		s, f := newService()
		raw(f, alice, "/shortcuts.json", `{"shortcuts":[]}`)
		_, err := s.Read(ctx, alice, wellknown.Shortcuts, "")
		Expect(err).NotTo(HaveOccurred())
		abs, _ := f.Abs(alice, "/LocalAI/shortcuts.json")
		hist, err := f.Store().History(ctx, f.Actor(alice), workspace.HistoryQuery{Path: abs, Limit: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(hist).NotTo(BeEmpty())
		Expect(hist[0].Title).To(Equal("Move shortcuts.json into LocalAI/"))
		_, _, err = f.Store().Undo(ctx, f.Actor(alice), hist[0].Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(exists(f, alice, "/shortcuts.json")).To(BeTrue())
		Expect(exists(f, alice, "/LocalAI/shortcuts.json")).To(BeFalse())
	})

	It("keeps the new file and leaves the old one alone when both were there", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/persona.json", `{"name":"New"}`)
		raw(f, alice, "/persona.json", `{"name":"Old"}`)
		doc, err := s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Data).To(HaveKeyWithValue("name", "New"))
		Expect(readRaw(f, alice, "/persona.json")).To(HaveKeyWithValue("name", "Old"))
		Expect(readRaw(f, alice, "/LocalAI/persona.json")).To(HaveKeyWithValue("name", "New"))
	})

	It("reads a file that is only in LocalAI/ and creates nothing at the top", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/home.json", `{"widgets":[]}`)
		doc, err := s.Read(ctx, alice, wellknown.Home, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Exists).To(BeTrue())
		Expect(exists(f, alice, "/home.json")).To(BeFalse())
	})

	It("picks up a file written at the old place after the move", func() {
		s, f := newService()
		raw(f, alice, "/user.json", `{"name":"Marta"}`)
		_, err := s.Read(ctx, alice, wellknown.User, "")
		Expect(err).NotTo(HaveOccurred())
		// An old page or a prompt from before writes the old path again.
		raw(f, alice, "/user.json", `{"name":"Marta","bio":"Designer"}`)
		doc, err := s.Read(ctx, alice, wellknown.User, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Data).To(HaveKeyWithValue("bio", "Designer"))
		Expect(exists(f, alice, "/user.json")).To(BeFalse())
		trash, err := f.Trash(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(trash).NotTo(BeEmpty())
		Expect(trash[0].Paths).To(ContainElement("/user.json"))
		// The content before the pick-up is still a version.
		vs, err := s.Versions(ctx, alice, wellknown.User, "", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(vs)).To(BeNumerically(">=", 2))
		Expect(vs[len(vs)-1].Data).NotTo(HaveKey("bio"))
	})

	It("moves every file once when two requests migrate at the same time", func() {
		s, f := newService()
		names := []wellknown.Name{wellknown.Persona, wellknown.User, wellknown.Shortcuts, wellknown.Home, wellknown.UI}
		bodies := map[wellknown.Name]string{
			wellknown.Persona: `{"name":"Ada"}`, wellknown.User: `{"name":"Marta"}`,
			wellknown.Shortcuts: `{"shortcuts":[]}`, wellknown.Home: `{"widgets":[]}`, wellknown.UI: `{"theme":"light"}`,
		}
		for _, n := range names {
			raw(f, alice, "/"+string(n)+".json", bodies[n])
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2*len(names))
		for range 2 {
			wg.Go(func() {
				defer GinkgoRecover()
				for _, n := range names {
					doc, err := s.Read(ctx, alice, n, "")
					if err == nil && !doc.Exists {
						err = errors.New(string(n) + " is missing")
					}
					errs <- err
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}
		dir, _ := f.Abs(alice, "/LocalAI")
		kids, err := f.Store().List(ctx, f.Actor(alice), dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(kids).To(HaveLen(len(names)), "no conflict copies")
		for _, n := range names {
			Expect(exists(f, alice, "/"+string(n)+".json")).To(BeFalse(), string(n))
		}
	})

	It("does nothing for a user with no settings files", func() {
		s, f := newService()
		s.AdoptTopLevelFiles(ctx, alice)
		for _, n := range []wellknown.Name{wellknown.Persona, wellknown.User, wellknown.Shortcuts, wellknown.Home, wellknown.UI} {
			doc, err := s.Read(ctx, alice, n, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(doc.Exists).To(BeFalse())
		}
		Expect(exists(f, alice, "/LocalAI")).To(BeFalse())
	})

	It("moves only the files of the user who asks, with that user's rights", func() {
		s, f := newService()
		raw(f, alice, "/persona.json", `{"name":"Ada"}`)
		doc, err := s.Read(ctx, "bob", wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Exists).To(BeFalse())
		Expect(exists(f, alice, "/persona.json")).To(BeTrue())
		s.AdoptTopLevelFiles(ctx, files.AdminPrincipal)
		s.AdoptTopLevelFiles(ctx, files.EveryoneGroup)
		Expect(exists(f, alice, "/persona.json")).To(BeTrue())
	})

	It("reads the old file when the move fails, and writes to LocalAI/", func() {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "workspace"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		f := files.New(noMoves{ws}, files.WithBlobs(blobs))
		Expect(f.EnsureHome(ctx, alice)).To(Succeed())
		s := wellknown.New(f)
		raw(f, alice, "/persona.json", `{"name":"Ada"}`)

		doc, err := s.Read(ctx, alice, wellknown.Persona, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Path).To(Equal("/persona.json"))
		Expect(doc.Data).To(HaveKeyWithValue("name", "Ada"))

		doc, err = s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Persona, Values: map[string]any{"system_prompt": "Be brief."}})
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Path).To(Equal("/LocalAI/persona.json"))
		// The keys of the old file came along.
		Expect(doc.Data).To(HaveKeyWithValue("name", "Ada"))
		Expect(readRaw(f, alice, "/persona.json")).To(HaveKeyWithValue("name", "Ada"))
	})
})
