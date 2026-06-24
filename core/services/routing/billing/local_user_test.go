package billing

import (
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocalUser", func() {
	It("persists ID", func() {
		// Reset the package-singleton sentinel so this test gets a fresh
		// LocalUser call. Without this, other tests racing through LocalUser
		// would freeze the value before we set DataPath.
		resetLocalUserForTesting()

		dir := GinkgoT().TempDir()
		u1 := LocalUser(dir)
		Expect(u1).NotTo(BeNil(), "LocalUser returned nil")
		Expect(u1.ID).NotTo(BeEmpty(), "LocalUser must have a non-empty ID")
		Expect(u1.Name).To(Equal(LocalUserName))

		// File written?
		idPath := filepath.Join(dir, localUserIDFile)
		got, err := os.ReadFile(idPath)
		Expect(err).NotTo(HaveOccurred(), "expected %s to exist", idPath)
		Expect(string(got)).To(Equal(u1.ID))

		// Singleton: subsequent calls return the same pointer.
		u2 := LocalUser(dir)
		Expect(u2).To(BeIdenticalTo(u1), "LocalUser returned a different instance on second call")
	})

	It("is stable across processes", func() {
		resetLocalUserForTesting()
		dir := GinkgoT().TempDir()

		first := LocalUser(dir).ID

		// Simulate process restart by clearing the singleton; the disk file
		// must let us recover the same UUID.
		resetLocalUserForTesting()

		second := LocalUser(dir).ID
		Expect(first).To(Equal(second), "local user id not stable across restart")
	})

	It("works with no data path", func() {
		resetLocalUserForTesting()
		u := LocalUser("")
		Expect(u).NotTo(BeNil())
		Expect(u.ID).NotTo(BeEmpty(), "LocalUser with empty data path must still produce a usable user")
	})
})

// resetLocalUserForTesting clears the package singleton so a test can
// rebind LocalUser to a fresh state. Tests must serialize on a mutex
// because Go tests within a package run concurrently within the same
// goroutine pool — LocalUser's sync.Once is a global, and these tests
// deliberately reach past it.
var testResetMu sync.Mutex

func resetLocalUserForTesting() {
	testResetMu.Lock()
	defer testResetMu.Unlock()
	localOnce = sync.Once{}
	localUser = nil
}
