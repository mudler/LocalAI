// SPDX-License-Identifier: MIT

package tracepersist_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
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

	It("stops writes under a released directory and leaves other stores alone", func() {
		root := GinkgoT().TempDir()
		inside, err := tracepersist.New[record](filepath.Join(root, "traces", "api"), 10)
		Expect(err).NotTo(HaveOccurred())
		outsideDir := GinkgoT().TempDir()
		outside, err := tracepersist.New[record](outsideDir, 10)
		Expect(err).NotTo(HaveOccurred())

		tracepersist.CloseUnder(root)

		Expect(inside.Closed()).To(BeTrue())
		Expect(inside.Append("1", record{ID: "1"})).To(MatchError(tracepersist.ErrClosed))
		Expect(inside.Clear()).To(MatchError(tracepersist.ErrClosed))
		entries, err := os.ReadDir(filepath.Join(root, "traces", "api"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())

		Expect(outside.Closed()).To(BeFalse())
		Expect(outside.Append("2", record{ID: "2"})).To(Succeed())
		Expect(outside.Load()).To(Equal([]record{{ID: "2"}}))
	})

	It("does not treat a sibling with a common prefix as inside the root", func() {
		parent := GinkgoT().TempDir()
		sibling, err := tracepersist.New[record](filepath.Join(parent, "data-other", "traces"), 10)
		Expect(err).NotTo(HaveOccurred())

		tracepersist.CloseUnder(filepath.Join(parent, "data"))

		Expect(sibling.Append("1", record{ID: "1"})).To(Succeed())
	})

	It("returns from CloseUnder only after in-flight writes finished", func() {
		root := GinkgoT().TempDir()
		dir := filepath.Join(root, "traces", "backend")
		store, err := tracepersist.New[record](dir, 1000)
		Expect(err).NotTo(HaveOccurred())

		var wg sync.WaitGroup
		started := make(chan struct{})
		var once sync.Once
		for w := range 8 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for i := 0; ; i++ {
					once.Do(func() { close(started) })
					err := store.Append(strconv.Itoa(w)+"-"+strconv.Itoa(i), record{ID: "x"})
					if errors.Is(err, tracepersist.ErrClosed) {
						return
					}
					Expect(err).NotTo(HaveOccurred())
				}
			}()
		}
		<-started
		tracepersist.CloseUnder(root)
		// Removing the directory right away is what a TempDir cleanup does;
		// it fails with "directory not empty" if a write is still running.
		Expect(os.RemoveAll(root)).To(Succeed())
		wg.Wait()
		_, err = os.Stat(root)
		Expect(os.IsNotExist(err)).To(BeTrue(), "no write recreated anything under the released root")
	})
})
