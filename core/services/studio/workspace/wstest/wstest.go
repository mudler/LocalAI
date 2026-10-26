// Package wstest has the conformance suite that every workspace.Store must
// pass, and helpers for tests that use a store.
package wstest

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Admin is the admin principal. A NewStore factory must open its store with
// this principal as the admin.
const Admin = "admin"

// Actors used by the suite.
var (
	AdminActor = workspace.Actor{Principal: Admin}
	Alice      = workspace.Actor{Principal: "user:alice"}
	AliceChat  = workspace.Actor{Principal: "user:alice", ChatID: "7"}
	Bob        = workspace.Actor{Principal: "user:bob", Groups: []string{"group:office"}}
)

// NewStore returns a new, empty store whose admin is Admin. It closes the
// store with t.Cleanup.
type NewStore func(t *testing.T) workspace.Store

// RunStoreSuite runs the conformance suite against stores from newStore.
func RunStoreSuite(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("Files", func(t *testing.T) { testFiles(t, newStore) })
	t.Run("Tx", func(t *testing.T) { testTx(t, newStore) })
	t.Run("Access", func(t *testing.T) { testAccess(t, newStore) })
	t.Run("ReadablePrefixes", func(t *testing.T) { testReadablePrefixes(t, newStore) })
	t.Run("Moves", func(t *testing.T) { testMoves(t, newStore) })
	t.Run("History", func(t *testing.T) { testHistory(t, newStore) })
	t.Run("Property", func(t *testing.T) { testProperty(t, newStore) })
	t.Run("Concurrency", func(t *testing.T) { testConcurrency(t, newStore) })
}

// Ctx returns the context the suite uses.
func Ctx() context.Context { return context.Background() }

// MustPut stores content at p, overwriting any file there.
func MustPut(t *testing.T, s workspace.Store, a workspace.Actor, p, content string) workspace.Node {
	t.Helper()
	n, err := s.Put(Ctx(), a, p, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("put %s: %v", p, err)
	}
	return n
}

// MustRead returns the content of the file at p.
func MustRead(t *testing.T, s workspace.Store, a workspace.Actor, p string) string {
	t.Helper()
	r, _, err := s.Open(Ctx(), a, p)
	if err != nil {
		t.Fatalf("open %s: %v", p, err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// Tree returns every live node as seen by the admin: a folder maps to "/",
// a file to its content.
func Tree(t *testing.T, s workspace.Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	var walk func(p string)
	walk = func(p string) {
		kids, err := s.List(Ctx(), AdminActor, p)
		if err != nil {
			t.Fatalf("list %s: %v", p, err)
		}
		for _, k := range kids {
			if k.Kind == workspace.KindDir {
				out[k.Path] = "/"
				walk(k.Path)
				continue
			}
			out[k.Path] = MustRead(t, s, AdminActor, k.Path)
		}
	}
	walk("/")
	return out
}

// latestRev returns the newest revision of the store, or 0 for an empty one.
func latestRev(t *testing.T, s workspace.Store) int64 {
	t.Helper()
	h, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(h) == 0 {
		return 0
	}
	return h[0].Rev
}
