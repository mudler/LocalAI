// SPDX-License-Identifier: MIT
package modelsprobe_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/modelsprobe"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelsProbe(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Models probe")
}

var _ = Describe("models probe", func() {
	It("reads back what it wrote with the same nonce", func() {
		dir := GinkgoT().TempDir()
		w, err := modelsprobe.Write(dir)
		Expect(err).NotTo(HaveOccurred())
		r, err := modelsprobe.Read(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(modelsprobe.Same(w, r)).To(BeTrue())
		Expect(r.Nonce).To(HaveLen(64))
	})

	It("uses a new nonce at every write", func() {
		dir := GinkgoT().TempDir()
		a, _ := modelsprobe.Write(dir)
		b, _ := modelsprobe.Write(dir)
		Expect(modelsprobe.Same(a, b)).To(BeFalse())
	})

	It("reports a missing file in another directory", func() {
		_, err := modelsprobe.Write(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		_, err = modelsprobe.Read(GinkgoT().TempDir())
		Expect(err).To(HaveOccurred())
	})

	It("differs when only the path differs", func() {
		dir := GinkgoT().TempDir()
		w, _ := modelsprobe.Write(dir)
		other := w
		other.ModelsPath = filepath.Join(dir, "elsewhere")
		Expect(modelsprobe.Same(w, other)).To(BeFalse())
	})

	It("refuses a partial or malformed file", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, modelsprobe.FileName), []byte(`{"nonce":"ab`), 0o644)).To(Succeed())
		_, err := modelsprobe.Read(dir)
		Expect(err).To(HaveOccurred())
	})

	It("leaves no temporary file behind", func() {
		dir := GinkgoT().TempDir()
		_, err := modelsprobe.Write(dir)
		Expect(err).NotTo(HaveOccurred())
		entries, _ := os.ReadDir(dir)
		Expect(entries).To(HaveLen(1))
	})
})
