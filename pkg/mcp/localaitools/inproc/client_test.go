package inproc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/system"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

var _ = Describe("inproc.Client model scheduling", func() {
	var (
		ctx      context.Context
		registry *nodes.NodeRegistry
		c        *Client
		stringp  = func(s string) *string { return &s }
		floatp   = func(f float64) *float64 { return &f }
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "nodes.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		tempDir := GinkgoT().TempDir()
		systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).ToNot(HaveOccurred())
		appConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
		c = New(appConfig, systemState, config.NewModelConfigLoader(tempDir), nil, nil, registry)
	})

	It("sets, lists, gets, merges, and deletes scheduling configs through the registry", func() {
		created, err := c.SetScheduling(ctx, localaitools.SetSchedulingRequest{
			ModelName:      "qwen",
			NodeSelector:   map[string]string{"gpu": "nvidia"},
			MinReplicas:    1,
			MaxReplicas:    2,
			RoutePolicy:    stringp("prefix_cache"),
			MinPrefixMatch: floatp(0.4),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(created.ModelName).To(Equal("qwen"))
		Expect(created.NodeSelector).To(Equal(`{"gpu":"nvidia"}`))
		Expect(created.RoutePolicy).To(Equal("prefix_cache"))
		Expect(created.MinPrefixMatch).To(Equal(0.4))
		Expect(schedulingJSONKeys(created)).ToNot(Or(
			HaveKey("id"),
			HaveKey("unsatisfiable_until"),
			HaveKey("unsatisfiable_ticks"),
			HaveKey("created_at"),
			HaveKey("updated_at"),
		))

		listed, err := c.ListScheduling(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(listed).To(HaveLen(1))
		Expect(listed[0].ModelName).To(Equal("qwen"))

		updated, err := c.SetScheduling(ctx, localaitools.SetSchedulingRequest{
			ModelName:   "qwen",
			MinReplicas: 2,
			MaxReplicas: 3,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.MinReplicas).To(Equal(2))
		Expect(updated.MaxReplicas).To(Equal(3))
		Expect(updated.RoutePolicy).To(Equal("prefix_cache"))
		Expect(updated.MinPrefixMatch).To(Equal(0.4))

		got, err := c.GetScheduling(ctx, "qwen")
		Expect(err).ToNot(HaveOccurred())
		Expect(got).ToNot(BeNil())
		Expect(got.MaxReplicas).To(Equal(3))

		Expect(c.DeleteScheduling(ctx, "qwen")).To(Succeed())
		missing, err := c.GetScheduling(ctx, "qwen")
		Expect(err).ToNot(HaveOccurred())
		Expect(missing).To(BeNil())
	})
})

func schedulingJSONKeys(config *localaitools.ModelSchedulingConfig) map[string]any {
	var out map[string]any
	Expect(json.Unmarshal([]byte(mustMarshal(config)), &out)).To(Succeed())
	return out
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	Expect(err).ToNot(HaveOccurred())
	return string(b)
}
