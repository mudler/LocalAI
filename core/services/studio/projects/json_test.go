package projects_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/projects/projectstest"
)

var _ = Describe("the JSON project store", func() {
	Describe("in memory", func() {
		projectstest.Run(func() projects.Store {
			s, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		projectstest.Run(func() projects.Store {
			s, err := projects.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("survives a reopen and keeps its file private", func() {
		dir := GinkgoT().TempDir()
		first, _ := projects.NewJSON(dir)
		projectstest.Seed(first)
		second, err := projects.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		projectstest.CheckSeed(second)
		st, err := os.Stat(filepath.Join(dir, "projects.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})
})
