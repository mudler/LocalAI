// SPDX-License-Identifier: MIT
package agentpool_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agentpool"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("legacy auth warning", func() {
	const warning = "User authentication is enabled but root agent data has no owner"
	var dir string
	var logFile *os.File

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		var err error
		logFile, err = os.CreateTemp(dir, "startup-*.log")
		Expect(err).NotTo(HaveOccurred())
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(logFile, nil), xlog.LogLevelInfo))
	})
	AfterEach(func() {
		xlog.SetLogger(xlog.NewLogger(xlog.LogLevelInfo, "text"))
		Expect(logFile.Close()).To(Succeed())
	})

	write := func(path, data string) {
		Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
		Expect(os.WriteFile(path, []byte(data), 0600)).To(Succeed())
	}
	start := func(enabled bool, collectionPath string) string {
		cfg := config.NewApplicationConfig()
		cfg.Auth.Enabled = enabled
		cfg.AgentPool = config.AgentPoolConfig{
			Enabled: true, StateDir: dir, APIURL: "http://127.0.0.1:1",
			DefaultModel: "unused", CollectionDBPath: collectionPath,
		}
		svc, err := agentpool.NewAgentPoolService(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.Start(context.Background())).To(Succeed())
		svc.Stop()
		data, err := os.ReadFile(logFile.Name())
		Expect(err).NotTo(HaveOccurred())
		return string(data)
	}

	It("warns about root agents, custom-path collections, skills and git repositories", func() {
		write(filepath.Join(dir, "pool.json"), `{"legacy":{"name":"legacy","model":"unused"},"owner:private":{"name":"owner:private","model":"unused"}}`)
		collectionsDir := filepath.Join(GinkgoT().TempDir(), "collections")
		write(filepath.Join(collectionsDir, "collection-notes.json"), `{}`)
		write(filepath.Join(dir, "skills", "legacy-skill", "SKILL.md"), "---\nname: legacy-skill\ndescription: A legacy skill\n---\nInstructions\n")
		write(filepath.Join(dir, "skills", ".git-repos.json"), `[{"id":"old","name":"old","url":"https://example.invalid/old.git","enabled":false}]`)
		logs := start(true, collectionsDir)
		Expect(logs).To(ContainSubstring(warning))
		Expect(logs).To(ContainSubstring("agents=1 collections=1 skills=1 git_repositories=1"))
		Expect(logs).To(ContainSubstring("authentication/#enabling-auth-on-an-existing-instance"))
		Expect(logs).NotTo(ContainSubstring("agents=2"))
		Expect(strings.Index(logs, warning)).To(BeNumerically("<", strings.Index(logs, "Creating agent")))
	})

	It("does not warn when auth is disabled", func() {
		write(filepath.Join(dir, "skills", "legacy-skill", "SKILL.md"), "---\nname: legacy-skill\ndescription: A legacy skill\n---\nInstructions\n")
		Expect(start(false, "")).NotTo(ContainSubstring(warning))
	})

	It("does not count user-owned data as root data", func() {
		write(filepath.Join(dir, "pool.json"), `{"owner:private":{"name":"owner:private","model":"unused"}}`)
		write(filepath.Join(dir, "users", "owner", "collections", "collection-notes.json"), `{}`)
		write(filepath.Join(dir, "users", "owner", "skills", "private", "SKILL.md"), "---\nname: private\ndescription: Private skill\n---\nInstructions\n")
		Expect(start(true, "")).NotTo(ContainSubstring(warning))
	})

	It("warns for configured git repositories even without installed skills", func() {
		write(filepath.Join(dir, "skills", ".git-repos.json"), `[{"id":"old","name":"old","url":"https://example.invalid/old.git","enabled":false}]`)
		logs := start(true, "")
		Expect(logs).To(ContainSubstring(warning))
		Expect(logs).To(ContainSubstring("agents=0 collections=0 skills=0 git_repositories=1"))
	})

	It("reports an unreadable repository config without preventing startup", func() {
		write(filepath.Join(dir, "collections", "collection-notes.json"), `{}`)
		write(filepath.Join(dir, "skills", ".git-repos.json"), `invalid json`)
		logs := start(true, "")
		Expect(logs).To(ContainSubstring("Could not check root skill repositories"))
		Expect(logs).To(ContainSubstring(warning))
		Expect(logs).To(ContainSubstring("agents=0 collections=1"))
	})
})
