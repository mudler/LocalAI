// SPDX-License-Identifier: MIT

package agentpool_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agentpool"
)

// The premise behind a deliberate non-decision: skills and collections get NO
// cross-replica invalidation, because both are derived from a directory that
// belongs to one frontend replica and no other replica reads or writes it. An
// invalidation broadcast cannot make a change visible in state that was never
// shared, so wiring one would report coherence the deployment does not have.
//
// This spec pins the premise rather than the absence. A change that moved
// either directory onto storage every replica mounts would redden it, and that
// is exactly the change after which the decision recorded in
// docs/content/features/distributed-mode.md ("Skills and collections are NOT
// replicated") has to be taken again.
var _ = Describe("skills and collections storage locality", func() {
	const (
		stateDir = "/var/lib/localai/agents"
		dataDir  = "/var/lib/localai/data"
		userID   = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
	)

	var storage *agentpool.UserScopedStorage

	BeforeEach(func() {
		storage = agentpool.NewUserScopedStorage(stateDir, dataDir)
	})

	It("keeps the cluster-wide skills and collections directories inside this replica's state directory", func() {
		Expect(storage.SkillsDir("")).To(Equal(filepath.Join(stateDir, "skills")))
		Expect(storage.CollectionsDir("")).To(Equal(filepath.Join(stateDir, "collections")))
	})

	It("keeps a tenant's skills and collections directories inside this replica's state directory", func() {
		Expect(storage.SkillsDir(userID)).To(Equal(filepath.Join(stateDir, "users", userID, "skills")))
		Expect(storage.CollectionsDir(userID)).To(Equal(filepath.Join(stateDir, "users", userID, "collections")))
	})

	// The assets a skill resource or an uploaded collection file lands in are
	// the same story: a peer that learned a collection had changed still could
	// not read the file the change is about.
	It("keeps a tenant's assets inside this replica's state directory", func() {
		Expect(storage.AssetsDir(userID)).To(Equal(filepath.Join(stateDir, "users", userID, "assets")))
	})
})
