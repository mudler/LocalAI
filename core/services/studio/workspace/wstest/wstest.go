// Package wstest has the conformance suite that every workspace.Store must
// pass, and helpers for tests that use a store.
package wstest

import (
	"context"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
// store with DeferCleanup, so it must be called from inside a spec.
type NewStore func() workspace.Store

// RunStoreSuite registers the conformance suite against stores from newStore.
// Call it at the top level of a Describe in the test package of the store; the
// package needs one Ginkgo suite bootstrap.
func RunStoreSuite(newStore NewStore) {
	describeFiles(newStore)
	describeTx(newStore)
	describeAccess(newStore)
	describeReadablePrefixes(newStore)
	describeGrants(newStore)
	describeMoves(newStore)
	describeHistory(newStore)
	describeProperty(newStore)
	describeConcurrency(newStore)
	describeMaterialize(newStore)
	describeUpper(newStore)
	describeUpperOwn(newStore)
	describeSandboxAccess(newStore)
}

// Ctx returns the context the suite uses.
func Ctx() context.Context { return context.Background() }

// MustPut stores content at p, overwriting any file there.
func MustPut(s workspace.Store, a workspace.Actor, p, content string) workspace.Node {
	GinkgoHelper()
	n, err := s.Put(Ctx(), a, p, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).ToNot(HaveOccurred(), "put %s", p)
	return n
}

// MustRead returns the content of the file at p.
func MustRead(s workspace.Store, a workspace.Actor, p string) string {
	GinkgoHelper()
	r, _, err := s.Open(Ctx(), a, p)
	Expect(err).ToNot(HaveOccurred(), "open %s", p)
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	Expect(err).ToNot(HaveOccurred(), "read %s", p)
	return string(b)
}

// Tree returns every live node as seen by the admin: a folder maps to "/",
// a file to its content.
func Tree(s workspace.Store) map[string]string {
	GinkgoHelper()
	out := map[string]string{}
	var walk func(p string)
	walk = func(p string) {
		kids, err := s.List(Ctx(), AdminActor, p)
		Expect(err).ToNot(HaveOccurred(), "list %s", p)
		for _, k := range kids {
			if k.Kind == workspace.KindDir {
				out[k.Path] = "/"
				walk(k.Path)
				continue
			}
			out[k.Path] = MustRead(s, AdminActor, k.Path)
		}
	}
	walk("/")
	return out
}
