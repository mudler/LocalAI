package studio

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

func capacityService() (*Service, workspace.Store, agents.Agent) {
	GinkgoHelper()
	ctx := context.Background()
	b, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), b, jsonstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	f := files.New(ws)
	Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
	_, err = ws.Mkdir(ctx, f.Actor("alice"), session.HomePath("alice")+"/Work")
	Expect(err).NotTo(HaveOccurred())
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	ag, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	s, err := New(Deps{Store: st, Agents: ag, Files: f, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = s.Close(ctx) })
	a, err := s.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "test"}, &AgentFolderWrite{Path: "/Work", Mode: agents.FolderRead})
	Expect(err).NotTo(HaveOccurred())
	return s, ws, a
}

func capacityShare(ws workspace.Store, i int) workspace.Node {
	GinkgoHelper()
	ctx := context.Background()
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	n, err := ws.Mkdir(ctx, admin, fmt.Sprintf("/shared/Share%02d", i))
	Expect(err).NotTo(HaveOccurred())
	Expect(ws.Grant(ctx, admin, files.EveryoneGroup, n.Path, workspace.ModeWrite)).To(Succeed())
	return n
}

var _ = Describe("folder capacity", func() {
	It("starts a scoped chat with more shared folders than fit and keeps the lowest IDs", func() {
		ctx := context.Background()
		s, ws, ag := capacityService()
		// Count live skill roots rather than assuming which optional layers exist.
		skills, dropped, err := s.readOnlyRoots(ctx, userChatActor("alice", ""), 1, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(dropped).To(BeZero())
		fit := workspace.MaxFenceRoots - 2 - len(skills)
		var shares []workspace.Node
		for i := 0; i < fit+8; i++ {
			shares = append(shares, capacityShare(ws, i))
		}
		rec, err := s.Create(ctx, "alice", CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred(), "the chat starts, it is not refused")
		x, err := s.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		for i, node := range shares {
			_, err := ws.Stat(ctx, x.Actor(), node.Path)
			if i < fit {
				Expect(err).NotTo(HaveOccurred(), "share %d is kept", i)
			} else {
				Expect(err).To(HaveOccurred(), "share %d is dropped, and nothing outside the layer is readable", i)
			}
		}
		Expect(x.View().NotMounted).To(Equal(8))
		Expect(session.LayoutPrompt(x.View(), session.PromptScope(x.View()))).To(ContainSubstring("8 shared folders are not mounted"))
		_, dropped, err = s.scopedRoots(ctx, userChatActor("alice", ""), []*folderScope{{folders: []scopedFolder{{node: 1, path: session.HomePath("alice") + "/Work"}}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(dropped).To(Equal(8))

		// A second frontend resolves the same set.
		secondDeps := s.d
		secondDeps.StateDir = GinkgoT().TempDir()
		second, err := New(secondDeps)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = second.Close(ctx) })
		y, err := second.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(y.View().NotMounted).To(Equal(8))
		Expect(x.Actor().SameFence(y.Actor())).To(BeTrue())
	})

	It("keeps a shared folder inside an agent folder out of the layer", func() {
		ctx := context.Background()
		s, ws, _ := capacityService()
		inside := capacityShare(ws, 1)
		roots, dropped, err := s.readOnlyRoots(ctx, userChatActor("alice", ""), 1, []string{"/shared"})
		Expect(err).NotTo(HaveOccurred())
		Expect(dropped).To(BeZero())
		for _, r := range roots {
			Expect(r.Node).NotTo(Equal(inside.ID))
		}
	})
})

// Each layer must carry every shared root; adding layers intersects rather
// than increasing capacity. Both an existing and a future chat root count.
var _ = Describe("folder capacity", func() {
	It("keeps the complete shared set in every intersecting scope", func() {
		base := userChatActor("alice", "chat")
		scopes := []*folderScope{{folders: []scopedFolder{{node: 1, mode: workspace.ModeWrite}}}, {folders: []scopedFolder{{node: 2, mode: workspace.ModeRead}}}}
		for _, chatNode := range []int64{0, 3} {
			roots := make([]workspace.FenceRoot, 30)
			for i := range roots {
				roots[i] = workspace.FenceRoot{Node: int64(i + 10), Mode: workspace.ModeRead}
			}
			a, err := fenceScopes(base, scopes, chatNode, roots)
			Expect(err).NotTo(HaveOccurred())
			got, ok := a.Fence()
			Expect(ok).To(BeTrue())
			width := 31
			if chatNode > 0 {
				width++
			}
			Expect(got).To(HaveLen(2 * width))
			for layer := 0; layer < 2; layer++ {
				for i, r := range roots {
					if got[layer*width+width-len(roots)+i] != r {
						Fail("shared set differs between layers")
					}
				}
			}
			roots = append(roots, workspace.FenceRoot{Node: 99, Mode: workspace.ModeRead})
			a, err = fenceScopes(base, scopes, chatNode, roots)
			Expect(err).To(MatchError(ErrAgentFolderLimit))
			Expect(a.Principal).To(BeEmpty())
		}
	})
})

type capacityFailStore struct{ workspace.Store }

func (s capacityFailStore) ReadablePrefixes(ctx context.Context, a workspace.Actor) ([]string, error) {
	return nil, errors.New("private enumeration failure")
}

var _ = Describe("folder capacity", func() {
	It("refuses scoped access when shared enumeration fails", func() {
		ctx := context.Background()
		s, ws, ag := capacityService()
		rec, err := s.Create(ctx, "alice", CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		s.d.Files = files.New(capacityFailStore{ws})
		// The shared service and resolver must use the same failing store.
		s.shared = sharedfolders.New(sharedfolders.Options{Files: s.d.Files})
		_, err = s.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).To(HaveOccurred(), "shared enumeration failure silently omitted roots")
		a := s.chatActorFor(ctx, rec)
		if roots, ok := a.Fence(); !ok || len(roots) != 0 {
			Fail("listing must fail closed")
		}
	})
})
