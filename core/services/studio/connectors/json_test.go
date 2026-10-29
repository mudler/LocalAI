package connectors_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connectors/connectorstest"
)

var _ = Describe("the JSON store", func() {
	Describe("in memory", func() {
		connectorstest.Run(func() connectors.Store {
			s, err := connectors.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		connectorstest.Run(func() connectors.Store {
			s, err := connectors.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("survives a reopen and keeps its files private", func() {
		dir := GinkgoT().TempDir()
		first, _ := connectors.NewJSON(dir)
		connectorstest.Seed(first)
		second, err := connectors.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		connectorstest.CheckSeed(second)
		for _, f := range []string{"connectors.json", "connectors.key"} {
			st, err := os.Stat(filepath.Join(dir, f))
			Expect(err).NotTo(HaveOccurred(), f)
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)), f)
		}
	})

	It("refuses a key file that others can read", func() {
		dir := GinkgoT().TempDir()
		first, _ := connectors.NewJSON(dir)
		connectorstest.Seed(first)
		Expect(os.Chmod(filepath.Join(dir, "connectors.key"), 0o644)).To(Succeed())
		second, err := connectors.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		_, err = second.EnsureKey(context.Background(), connectors.NewKey)
		Expect(err).To(HaveOccurred(), "a key file with wider permissions was used")
	})
})
