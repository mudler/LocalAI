// SPDX-License-Identifier: MIT

package tracepersist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/trace/tracepersist"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTracePersist(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Trace persistence store suite")
}

type record struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

var _ = Describe("Store", func() {
	It("loads records oldest-first and skips corrupt files", func() {
		dir := GinkgoT().TempDir()
		store, err := tracepersist.New[record](dir, 3)
		Expect(err).NotTo(HaveOccurred())

		Expect(store.Append("1", record{ID: "1", Value: "first"})).To(Succeed())
		Expect(store.Append("2", record{ID: "2", Value: "second"})).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "00000000000000000003.json"), []byte("{"), 0o600)).To(Succeed())

		records, err := store.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(Equal([]record{
			{ID: "1", Value: "first"},
			{ID: "2", Value: "second"},
		}))
	})

	It("evicts the oldest records on disk", func() {
		dir := GinkgoT().TempDir()
		store, err := tracepersist.New[record](dir, 2)
		Expect(err).NotTo(HaveOccurred())

		Expect(store.Append("8", record{ID: "8"})).To(Succeed())
		Expect(store.Append("9", record{ID: "9"})).To(Succeed())
		Expect(store.Append("10", record{ID: "10"})).To(Succeed())

		records, err := store.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(Equal([]record{{ID: "9"}, {ID: "10"}}))
	})

	It("clears only files owned by the store", func() {
		parent := GinkgoT().TempDir()
		dir := filepath.Join(parent, "api")
		store, err := tracepersist.New[record](dir, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Append("1", record{ID: "1"})).To(Succeed())
		other := filepath.Join(parent, "keep")
		Expect(os.WriteFile(other, []byte("keep"), 0o600)).To(Succeed())

		Expect(store.Clear()).To(Succeed())

		records, err := store.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
		Expect(other).To(BeAnExistingFile())
	})
})
