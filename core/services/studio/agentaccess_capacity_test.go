package studio

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

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

func capacityService(t *testing.T) (*Service, workspace.Store, agents.Agent) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := jsonstore.New(filepath.Join(t.TempDir(), "ws"), b, jsonstore.Options{Admin: files.AdminPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	f := files.New(ws)
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Mkdir(ctx, f.Actor("alice"), session.HomePath("alice")+"/Work"); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	ag, err := agents.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Deps{Store: st, Agents: ag, Files: f, Provider: provider.New(), StateDir: t.TempDir(), Harness: studiotest.NewGateHarness()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(ctx) })
	a, err := s.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "test"}, &AgentFolderWrite{Path: "/Work", Mode: agents.FolderRead})
	if err != nil {
		t.Fatal(err)
	}
	return s, ws, a
}

func capacityShare(t *testing.T, ws workspace.Store, i int) workspace.Node {
	t.Helper()
	ctx := context.Background()
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	n, err := ws.Mkdir(ctx, admin, fmt.Sprintf("/shared/Share%02d", i))
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Grant(ctx, admin, files.EveryoneGroup, n.Path, workspace.ModeWrite); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAgentFolderCapacity(t *testing.T) {
	ctx := context.Background()
	s, ws, ag := capacityService(t)
	// Count live skill roots rather than assuming which optional layers exist.
	roots, err := s.readOnlyRoots(ctx, userChatActor("alice", ""))
	if err != nil {
		t.Fatal(err)
	}
	n := workspace.MaxFenceRoots - 2 - len(roots)
	var shares []workspace.Node
	for i := 0; i < n; i++ {
		shares = append(shares, capacityShare(t, ws, i))
	}
	rec, err := s.Create(ctx, "alice", CreateInput{AgentID: ag.ID})
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.ChatAccess(ctx, "alice", rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range shares {
		if _, err := ws.Stat(ctx, x.Actor(), node.Path); err != nil {
			t.Fatalf("required root %s unreadable: %v", node.Path, err)
		}
	}
	extra := capacityShare(t, ws, n)
	// An existing chat must refuse its next resolution instead of losing one share.
	if x, err := s.ChatAccess(ctx, "alice", rec.ID); !errors.Is(err, ErrAgentFolderLimit) || x.Actor().Principal != "" {
		t.Error("33 required roots must refuse, not truncate")
	}
	if _, err := s.CommandActor(ctx, "alice", rec.ID); err == nil {
		t.Error("command must refuse before materializing")
	}
	if _, err := s.Create(ctx, "alice", CreateInput{AgentID: ag.ID}); err == nil {
		t.Error("create must refuse before saving a chat")
	}
	if _, err := s.AddonAccess(ctx, "alice", rec.ID); !errors.Is(err, ErrAgentFolderLimit) {
		t.Fatal("bound add-on must refuse")
	}
	if _, err := s.SendBackground(ctx, "alice", rec.ID, "hi", nil); !errors.Is(err, ErrAgentFolderLimit) {
		t.Fatal("background turn must refuse")
	}
	// Even before a chat folder exists, reserve its slot and list no tools.
	pending := session.ChatRecord{ID: "pending", Principal: "alice", AgentID: ag.ID}
	actor := s.chatActorFor(ctx, pending)
	if roots, fenced := actor.Fence(); !fenced || len(roots) != 0 {
		t.Error("over-budget pre-turn actor must be fenced empty")
	}
	if err := ws.Revoke(ctx, workspace.Actor{Principal: files.AdminPrincipal}, files.EveryoneGroup, extra.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChatAccess(ctx, "alice", rec.ID); err != nil {
		t.Fatalf("retry after share removal: %v", err)
	}
	if _, err := s.Create(ctx, "alice", CreateInput{Model: "test"}); err != nil {
		t.Fatalf("unscoped chat changed: %v", err)
	}
}

// Each layer must carry every shared root; adding layers intersects rather
// than increasing capacity. Both an existing and a future chat root count.
func TestFenceScopesCapacity(t *testing.T) {
	base := userChatActor("alice", "chat")
	scopes := []folderScope{{node: 1, mode: workspace.ModeWrite}, {node: 2, mode: workspace.ModeRead}}
	for _, chatNode := range []int64{0, 3} {
		roots := make([]workspace.FenceRoot, 30)
		for i := range roots {
			roots[i] = workspace.FenceRoot{Node: int64(i + 10), Mode: workspace.ModeRead}
		}
		a, err := fenceScopes(base, scopes, chatNode, roots)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := a.Fence()
		if !ok {
			t.Fatal("unfenced actor")
		}
		width := 31
		if chatNode > 0 {
			width++
		}
		if len(got) != 2*width {
			t.Fatal("lost roots in one of the intersecting layers")
		}
		for layer := 0; layer < 2; layer++ {
			for i, r := range roots {
				if got[layer*width+width-len(roots)+i] != r {
					t.Fatal("shared set differs between layers")
				}
			}
		}
		roots = append(roots, workspace.FenceRoot{Node: 99, Mode: workspace.ModeRead})
		a, err = fenceScopes(base, scopes, chatNode, roots)
		if !errors.Is(err, ErrAgentFolderLimit) || a.Principal != "" {
			t.Fatal("capacity must return typed error without partial actor")
		}
	}
}

type capacityFailStore struct{ workspace.Store }

func (s capacityFailStore) ReadablePrefixes(ctx context.Context, a workspace.Actor) ([]string, error) {
	return nil, errors.New("private enumeration failure")
}

func TestCapacityEnumerationFailure(t *testing.T) {
	ctx := context.Background()
	s, ws, ag := capacityService(t)
	rec, err := s.Create(ctx, "alice", CreateInput{AgentID: ag.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.d.Files = files.New(capacityFailStore{ws})
	// The shared service and resolver must use the same failing store.
	s.shared = sharedfolders.New(sharedfolders.Options{Files: s.d.Files})
	if _, err := s.ChatAccess(ctx, "alice", rec.ID); err == nil {
		t.Fatal("shared enumeration failure silently omitted roots")
	}
	a := s.chatActorFor(ctx, rec)
	if roots, ok := a.Fence(); !ok || len(roots) != 0 {
		t.Fatal("listing must fail closed")
	}
}
