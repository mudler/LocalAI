package agents_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

var _ = Describe("the JSON agent store", func() {
	Describe("in memory", func() {
		agentstest.Run(func() agents.Store {
			s, err := agents.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		agentstest.Run(func() agents.Store {
			s, err := agents.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("reads an agent stored before the folder fields as one without a folder", func() {
		dir := GinkgoT().TempDir()
		old := `[{"ID":"old","Principal":"u1","Name":"Old","Description":"","Instructions":"","Model":"","MCPServers":null,"Addons":null,"Collections":null,"Skills":null,"Network":"","Created":"2026-10-01T09:00:00Z","Updated":"2026-10-01T09:00:00Z"}]`
		Expect(os.WriteFile(filepath.Join(dir, "agents.json"), []byte(old), 0o600)).To(Succeed())
		s, err := agents.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		got, err := s.Get(context.Background(), "u1", "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Old"))
		Expect(got.FolderID).To(BeZero())
		Expect(got.FolderMode).To(BeEmpty())
	})

	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := agents.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		agentstest.Seed(first)
		second, err := agents.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		agentstest.CheckSeed(second)
	})
})
