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
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/failover"
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

// fakeFailoverSource is a minimal failover.ConfigSource over an in-memory
// map, so these specs don't need a real ModelConfigLoader + on-disk YAML.
type fakeFailoverSource struct {
	cfgs map[string]config.ModelConfig
}

func (s *fakeFailoverSource) GetModelConfig(name string) (config.ModelConfig, bool) {
	c, ok := s.cfgs[name]
	return c, ok
}

func (s *fakeFailoverSource) GetAllModelsConfigs() []config.ModelConfig {
	out := make([]config.ModelConfig, 0, len(s.cfgs))
	for _, c := range s.cfgs {
		out = append(out, c)
	}
	return out
}

var _ = Describe("inproc.Client failover chains", func() {
	var (
		ctx context.Context
		c   *Client
		fm  *failover.Manager
	)

	BeforeEach(func() {
		ctx = context.Background()
		src := &fakeFailoverSource{cfgs: map[string]config.ModelConfig{
			"a": {Name: "a", Backend: "llama-cpp"},
			"b": {Name: "b", Backend: "llama-cpp"},
			"chain": {Name: "chain", Failover: &config.FailoverConfig{
				Targets: []config.FailoverTarget{{Model: "a"}, {Model: "b"}},
			}},
		}}
		fm = failover.New(src)
		c = &Client{Failover: fm}
	})

	It("ListFailoverChains reports the chain, its active target, and target health", func() {
		out, err := c.ListFailoverChains(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(out[0].Name).To(Equal("chain"))
		Expect(out[0].Active).To(Equal("a"))
		Expect(out[0].Pinned).To(BeEmpty())
		Expect(out[0].Targets).To(HaveLen(2))
		Expect(out[0].Targets[0].Model).To(Equal("a"))
		Expect(out[0].Targets[0].Kind).To(Equal("local"))
		Expect(out[0].Targets[0].State).To(Equal("healthy"))
	})

	It("returns an empty slice, not an error, when no failover manager is wired", func() {
		c = &Client{}
		out, err := c.ListFailoverChains(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(BeEmpty())
	})

	It("PinFailoverTarget pins the chain and ListFailoverChains reflects it", func() {
		Expect(c.PinFailoverTarget(ctx, "chain", "b")).To(Succeed())

		out, err := c.ListFailoverChains(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(out[0].Pinned).To(Equal("b"))
	})

	It("PinFailoverTarget errors when the failover manager is unavailable", func() {
		c = &Client{}
		err := c.PinFailoverTarget(ctx, "chain", "b")
		Expect(err).To(HaveOccurred())
	})

	It("UnpinFailoverTarget clears a pin", func() {
		Expect(c.PinFailoverTarget(ctx, "chain", "b")).To(Succeed())
		Expect(c.UnpinFailoverTarget(ctx, "chain")).To(Succeed())

		out, err := c.ListFailoverChains(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(out[0].Pinned).To(BeEmpty())
	})

	It("UnpinFailoverTarget errors when the failover manager is unavailable", func() {
		c = &Client{}
		err := c.UnpinFailoverTarget(ctx, "chain")
		Expect(err).To(HaveOccurred())
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

type fakeCarrierSource struct {
	report cluster.Report
	err    error
}

func (f fakeCarrierSource) Status(context.Context) (cluster.Report, error) { return f.report, f.err }

var _ = Describe("GetClusterCarrier", func() {
	It("reports distributed=false when no switch is wired", func() {
		out, err := (&Client{}).GetClusterCarrier(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(out.Distributed).To(BeFalse())
	})

	It("maps the report to the assistant view", func() {
		c := &Client{Carrier: fakeCarrierSource{report: cluster.Report{
			Active: cluster.CarrierTunnel, State: cluster.StateStable, Epoch: 4,
			DrainRemaining: 90 * time.Second,
			Replicas:       []cluster.ReplicaStatus{{ID: "r1", Version: "v1", ReadyEpoch: 4}},
			Workers: []cluster.WorkerStatus{{ID: "w1", Name: "gpu", Attached: []cluster.Carrier{cluster.CarrierTunnel},
				CanFollow: false, Reason: "no routable address", FollowError: "no address"}},
		}}}
		out, err := c.GetClusterCarrier(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(out.Distributed).To(BeTrue())
		Expect(out.Active).To(Equal("tunnel"))
		Expect(out.Epoch).To(Equal(int64(4)))
		Expect(out.DrainRemainingSeconds).To(BeNumerically("==", 90))
		Expect(out.Replicas).To(ConsistOf(localaitools.ClusterReplicaInfo{ID: "r1", Version: "v1", ReadyEpoch: 4}))
		Expect(out.Workers).To(HaveLen(1))
		Expect(out.Workers[0].Attached).To(Equal([]string{"tunnel"}))
		Expect(out.Workers[0].CanFollow).To(BeFalse())
		Expect(out.Workers[0].FollowError).To(Equal("no address"))
	})

	It("passes an error from the switch on", func() {
		_, err := (&Client{Carrier: fakeCarrierSource{err: errors.New("db down")}}).GetClusterCarrier(context.Background())
		Expect(err).To(MatchError(ContainSubstring("db down")))
	})
})
