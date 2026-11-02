// SPDX-License-Identifier: MIT
package packages

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
)

var _ = Describe("Refused package stages", func() {
	It("discards only the creating installer's unpublished single use stage", func() {
		manifest, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		i, r := fixture(archive(nil, manifest))
		staged, e := i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
		dir := staged.state.dir
		other := &Installer{}
		Expect(other.Discard(staged)).NotTo(Succeed())
		Expect(dir).To(BeADirectory())
		Expect(i.Discard(staged)).To(Succeed())
		Expect(dir).NotTo(BeADirectory())
		_, e = i.Commit(context.Background(), staged)
		Expect(e).To(HaveOccurred())
		Expect(i.Discard(staged)).NotTo(Succeed())
	})
})
