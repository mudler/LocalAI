package messaging_test

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// moduleFile reads a file from the repository root, located from this source
// file rather than from the working directory, which Ginkgo does not promise.
func moduleFile(name string) string {
	_, thisFile, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue(), "could not locate this source file, so the assertion below would read nothing")
	dir := filepath.Dir(thisFile)
	for {
		candidate := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(candidate); err == nil {
			body, err := os.ReadFile(filepath.Join(dir, name))
			Expect(err).ToNot(HaveOccurred())
			return string(body)
		}
		parent := filepath.Dir(dir)
		Expect(parent).ToNot(Equal(dir), "walked past the filesystem root without finding go.mod")
		dir = parent
	}
}

// The point of the whole removal, asserted as a property of the module rather
// than as a property of any one package.
//
// Every other guard in this repository is a guard on a call site: a deleted
// method, a deleted parameter, a deleted field. None of them can say that the
// DEPENDENCY is gone, because a dependency survives on a single blank import in
// a single test file in a package nobody reads, and `go mod tidy` succeeding
// proves nothing about that: tidy keeps a require exactly when something still
// reaches it.
//
// This is the assertion that reddens when it comes back. It reads the module
// files themselves, so it is blind to build tags, to GOOS, and to whether a
// package can be loaded at all, which is what the source-level greps cannot
// promise and the dependency walks cannot promise either.
var _ = Describe("the module's dependency on a message broker", func() {
	It("requires no nats-io module", func() {
		Expect(moduleFile("go.mod")).ToNot(ContainSubstring("github.com/nats-io/"),
			"a nats-io module is required again: nothing in this deployment dials a message bus, so a require is a family being invited back onto a carrier nothing reads")
	})

	It("requires no NATS testcontainer", func() {
		// Named separately because it comes back by a different route: a spec
		// standing a broker up for a suite, rather than production code
		// dialling one. It is also the reference a dependency walk WITHOUT
		// -test reports as absent while the suite is still starting a
		// nats:2-alpine container.
		Expect(moduleFile("go.mod")).ToNot(ContainSubstring("testcontainers-go/modules/nats"),
			"a suite is standing a broker up again")
	})

	It("carries no nats-io checksum", func() {
		// go.sum outlives go.mod by a release when a require is dropped by hand
		// rather than by tidy, so it is asserted on its own: a checksum left
		// behind is the evidence that the removal was partial.
		Expect(moduleFile("go.sum")).ToNot(ContainSubstring("github.com/nats-io/"))
	})
})
