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
	It("preserves 32 roots and refuses 33 until access changes", func() {
		ctx := context.Background()
		s, ws, ag := capacityService()
		// Count live skill roots rather than assuming which optional layers exist.
		roots, err := s.readOnlyRoots(ctx, userChatActor("alice", ""))
		Expect(err).NotTo(HaveOccurred())
		n := workspace.MaxFenceRoots - 2 - len(roots)
		var shares []workspace.Node
		for i := 0; i < n; i++ {
			shares = append(shares, capacityShare(ws, i))
		}
		rec, err := s.Create(ctx, "alice", CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		x, err := s.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		for _, node := range shares {
			if _, err := ws.Stat(ctx, x.Actor(), node.Path); err != nil {
				Fail(fmt.Sprintf("required root %s unreadable: %v", node.Path, err))
			}
		}
		harness := s.d.Harness.(*studiotest.GateHarness)
		finishTurn := func(svc *Service, text string) {
			GinkgoHelper()
			harness.Gate <- struct{}{}
			_, err := svc.Send(ctx, "alice", rec.ID, text)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() bool {
				turn, err := svc.Turn(ctx, "alice", rec.ID)
				return err == nil && !turn.Running && turn.Last == store.TurnDone
			}).Should(BeTrue())
		}
		finishTurn(s, "first successful turn")
		secondDeps := s.d
		secondDeps.StateDir = GinkgoT().TempDir()
		second, err := New(secondDeps)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = second.Close(ctx) })
		extra := capacityShare(ws, n)
		// An existing chat must refuse its next resolution instead of losing one share.
		x, err = s.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).To(MatchError(ErrAgentFolderLimit))
		Expect(x.Actor().Principal).To(BeEmpty(), "no partial actor")
		_, err = s.CommandActor(ctx, "alice", rec.ID)
		Expect(err).To(HaveOccurred(), "command must refuse before materializing")
		_, err = s.Create(ctx, "alice", CreateInput{AgentID: ag.ID})
		Expect(err).To(HaveOccurred(), "create must refuse before saving a chat")
		_, err = s.AddonAccess(ctx, "alice", rec.ID)
		Expect(err).To(MatchError(ErrAgentFolderLimit), "bound add-on must refuse")
		_, err = s.SendBackground(ctx, "alice", rec.ID, "hi", nil)
		Expect(err).To(MatchError(ErrAgentFolderLimit), "background turn must refuse")
		_, err = second.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).To(MatchError(ErrAgentFolderLimit))
		_, err = second.Send(ctx, "alice", rec.ID, "refused next turn")
		Expect(err).To(MatchError(ErrAgentFolderLimit))
		turn, err := second.Turn(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn.Running).To(BeFalse(), "refusal must not retain a turn claim")
		waiting, err := second.Pending(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(waiting).To(BeEmpty(), "refusal must not queue the message")
		// Even before a chat folder exists, reserve its slot and list no tools.
		pending := session.ChatRecord{ID: "pending", Principal: "alice", AgentID: ag.ID}
		actor := s.chatActorFor(ctx, pending)
		if roots, fenced := actor.Fence(); !fenced || len(roots) != 0 {
			Fail("over-budget pre-turn actor must be fenced empty")
		}
		Expect(ws.Revoke(ctx, workspace.Actor{Principal: files.AdminPrincipal}, files.EveryoneGroup, extra.Path)).To(Succeed())
		if _, err := s.ChatAccess(ctx, "alice", rec.ID); err != nil {
			Fail(fmt.Sprintf("retry after share removal: %v", err))
		}
		_, err = second.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		finishTurn(second, "successful retry on another frontend")
		if _, err := s.Create(ctx, "alice", CreateInput{Model: "test"}); err != nil {
			Fail(fmt.Sprintf("unscoped chat changed: %v", err))
		}
	})
})

// Each layer must carry every shared root; adding layers intersects rather
// than increasing capacity. Both an existing and a future chat root count.
var _ = Describe("folder capacity", func() {
	It("keeps the complete shared set in every intersecting scope", func() {
		base := userChatActor("alice", "chat")
		scopes := []folderScope{{node: 1, mode: workspace.ModeWrite}, {node: 2, mode: workspace.ModeRead}}
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
