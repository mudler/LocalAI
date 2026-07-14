package inproc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/system"
)

// Regression spec for the bug we fixed when channel sends were
// unconditional: with a never-read gallery channel and a pre-cancelled
// ctx, InstallModel must surface ctx.Err() instead of blocking forever.
// The same guarantee covers ImportModelURI, DeleteModel, InstallBackend,
// UpgradeBackend — they all share sendModelOp / sendBackendOp.
var _ = Describe("inproc.Client cancellation", func() {
	It("InstallModel returns context.Canceled when the gallery channel is never drained", func() {
		gs := &galleryop.GalleryService{
			// Unbuffered. Nothing reads from it in this spec, so a naive
			// send would block the goroutine indefinitely.
			ModelGalleryChannel: make(chan galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]),
		}
		c := &Client{
			AppConfig:   &config.ApplicationConfig{SystemState: &system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}}},
			SystemState: &system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}},
			Gallery:     gs,
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel: the select must take the ctx.Done branch immediately.

		done := make(chan error, 1)
		go func() {
			_, err := c.InstallModel(ctx, localaitools.InstallModelRequest{ModelName: "x"})
			done <- err
		}()

		var err error
		Eventually(done, time.Second).Should(Receive(&err))
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "got: %v", err)
	})
})

var _ = Describe("inproc.Client model aliases", func() {
	var (
		ctx       context.Context
		tempDir   string
		cl        *config.ModelConfigLoader
		c         *Client
		seedModel func(name, body string)
	)

	BeforeEach(func() {
		ctx = context.Background()
		tempDir = GinkgoT().TempDir()
		systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).ToNot(HaveOccurred())
		appConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
		cl = config.NewModelConfigLoader(tempDir)
		// Gallery/model loaders are unused by the alias methods, so nil is fine.
		c = New(appConfig, systemState, cl, nil, nil)

		seedModel = func(name, body string) {
			Expect(os.WriteFile(filepath.Join(tempDir, name+".yaml"), []byte(body), 0644)).To(Succeed())
			Expect(cl.LoadModelConfigsFromPath(tempDir)).To(Succeed())
		}
	})

	Describe("ListAliases", func() {
		It("returns only configs whose alias field is set", func() {
			seedModel("real", "name: real\nbackend: llama-cpp\n")
			seedModel("gpt-4", "name: gpt-4\nalias: real\n")

			out, err := c.ListAliases(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(ConsistOf(localaitools.AliasInfo{Name: "gpt-4", Target: "real"}))
		})

		It("returns an empty slice when there are no aliases", func() {
			seedModel("real", "name: real\nbackend: llama-cpp\n")
			out, err := c.ListAliases(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(BeEmpty())
		})
	})

	Describe("SetAlias", func() {
		It("creates a new alias config on disk when the name is unused", func() {
			seedModel("real", "name: real\nbackend: llama-cpp\n")

			Expect(c.SetAlias(ctx, "gpt-4", "real")).To(Succeed())

			Expect(filepath.Join(tempDir, "gpt-4.yaml")).To(BeAnExistingFile())
			out, err := c.ListAliases(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(ConsistOf(localaitools.AliasInfo{Name: "gpt-4", Target: "real"}))
		})

		It("swaps an existing alias's target in place", func() {
			seedModel("real", "name: real\nbackend: llama-cpp\n")
			seedModel("other", "name: other\nbackend: llama-cpp\n")
			seedModel("gpt-4", "name: gpt-4\nalias: real\n")

			Expect(c.SetAlias(ctx, "gpt-4", "other")).To(Succeed())

			out, err := c.ListAliases(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(ConsistOf(localaitools.AliasInfo{Name: "gpt-4", Target: "other"}))
		})

		It("rejects an alias whose target does not exist", func() {
			err := c.SetAlias(ctx, "gpt-4", "missing")
			Expect(err).To(HaveOccurred())
			Expect(filepath.Join(tempDir, "gpt-4.yaml")).ToNot(BeAnExistingFile())
		})
	})
})
