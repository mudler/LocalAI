package remotes_test

import (
	"context"
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/core/services/remotes"
)

// fakeChains answers the live state of chains from a map.
type fakeChains map[string]failover.ChainStatus

func (f fakeChains) ChainStatus(name string) (failover.ChainStatus, bool) {
	st, ok := f[name]
	return st, ok
}

var _ = Describe("Federation", func() {
	var (
		ctx    = context.Background()
		remote *fakeRemote
		e      *env
		fed    remotes.Federation
		chains fakeChains
		branch remotes.Remote
	)

	BeforeEach(func() {
		remote = newFakeRemote("qwen", "gemma")
		DeferCleanup(remote.Close)
		store := memStore()
		e = newEnv(store)
		chains = fakeChains{}
		var err error
		fed, err = remotes.NewFederation(remotes.Options{
			Store: store, Admin: e.admin, Configs: e.loader, Broadcaster: e.bus, ModelsPath: e.dir,
		}, chains)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(e.file("qwen-local"), []byte("name: qwen-local\nbackend: llama-cpp\n"), 0o600)).To(Succeed())
		Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
		// The remote's qwen gets the plain name "qwen".
		res, err := e.svc.Create(ctx, remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Branch", URL: remote.URL, APIKey: secret, Models: chat("qwen")})
		Expect(err).NotTo(HaveOccurred())
		branch = res.Remote
	})

	create := func(members ...string) remotes.Group {
		g, err := fed.CreateGroup(ctx, remotes.GroupRequest{Name: "shared-qwen", Members: members})
		Expect(err).NotTo(HaveOccurred())
		return g
	}

	It("makes a group as a balanced chain of this server's model and the remote's entry", func() {
		g := create("qwen-local", "qwen")
		Expect(g.Name).To(Equal("shared-qwen"))
		Expect(g.Policy).To(Equal(config.FailoverPolicyLeastBusy))
		Expect(g.Members).To(HaveLen(2))
		Expect(g.Members[0]).To(Equal(remotes.GroupMember{Model: "qwen-local", Instance: remotes.LocalInstance, UpstreamID: "qwen-local"}))
		Expect(g.Members[1].Instance).To(Equal(branch.ID))
		Expect(g.Members[1].InstanceName).To(Equal("Branch"))
		Expect(g.Members[1].UpstreamID).To(Equal("qwen"))

		cfg := e.config("shared-qwen")
		Expect(cfg.IsFailover()).To(BeTrue())
		Expect(cfg.Failover.Policy).To(Equal(config.FailoverPolicyLeastBusy))
		Expect(e.read("shared-qwen")).To(ContainSubstring("federation group"))
		Expect(e.bus.ops("shared-qwen")).To(Equal([]string{"install"}))
	})

	It("lists this server and the LocalAI remotes as instances with their models", func() {
		create("qwen-local", "qwen")
		v, err := fed.View(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Instances).To(HaveLen(2))
		Expect(v.Instances[0].Local).To(BeTrue())
		Expect(v.Instances[0].Models).To(ContainElement(remotes.InstanceModel{Model: "qwen-local", UpstreamID: "qwen-local"}))
		// The group and the remote's entries are not models of this server.
		Expect(v.Instances[0].Models).NotTo(ContainElement(HaveField("Model", "shared-qwen")))
		Expect(v.Instances[0].Models).NotTo(ContainElement(HaveField("Model", "qwen")))
		Expect(v.Instances[1].ID).To(Equal(branch.ID))
		Expect(v.Instances[1].Models).To(Equal([]remotes.InstanceModel{{Model: "qwen", UpstreamID: "qwen"}}))
		Expect(v.Groups).To(HaveLen(1))
	})

	It("adds the live state of the chain and sums requests in flight per instance", func() {
		create("qwen-local", "qwen")
		chains["shared-qwen"] = failover.ChainStatus{Name: "shared-qwen", State: failover.ChainPrimary, Targets: []failover.TargetStatus{
			{Model: "qwen-local", State: failover.StateHealthy, InFlight: 2},
			{Model: "qwen", State: failover.StateDown, InFlight: 1, Constrained: true, LastError: "relay limit"},
		}}
		v, err := fed.View(ctx)
		Expect(err).NotTo(HaveOccurred())
		g := v.Groups[0]
		Expect(g.State).To(Equal("primary"))
		Expect(g.Members[1].State).To(Equal("down"))
		Expect(g.Members[1].Constrained).To(BeTrue())
		Expect(g.Members[1].LastError).To(Equal("relay limit"))
		Expect(v.Instances[0].InFlight).To(Equal(2))
		Expect(v.Instances[1].InFlight).To(Equal(1))
	})

	It("changes the routing and the members and keeps the drain flag of the models that stay", func() {
		create("qwen-local", "qwen")
		Expect(os.WriteFile(e.file("qwen-two"), []byte("name: qwen-two\nbackend: llama-cpp\n"), 0o600)).To(Succeed())
		Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
		_, err := fed.Drain(ctx, "shared-qwen", "qwen", true)
		Expect(err).NotTo(HaveOccurred())

		rr := config.FailoverPolicyRoundRobin
		members := []string{"qwen", "qwen-two"}
		g, err := fed.UpdateGroup(ctx, "shared-qwen", remotes.GroupUpdate{Policy: &rr, Members: &members})
		Expect(err).NotTo(HaveOccurred())
		Expect(g.Policy).To(Equal(rr))
		Expect(g.Members).To(HaveLen(2))
		Expect(g.Members[0].Model).To(Equal("qwen"))
		Expect(g.Members[0].Drained).To(BeTrue())
		Expect(g.Members[1].Model).To(Equal("qwen-two"))
		Expect(g.Members[1].Drained).To(BeFalse())
		Expect(e.read("shared-qwen")).To(ContainSubstring("round_robin"))
	})

	It("keeps the other settings of the file when it changes the group", func() {
		create("qwen-local", "qwen")
		_, err := e.admin.PatchConfig(ctx, "shared-qwen", map[string]any{"failover": map[string]any{"trip": map[string]any{"errors": 3}}, "description": "kept"})
		Expect(err).NotTo(HaveOccurred())
		rr := config.FailoverPolicyRoundRobin
		_, err = fed.UpdateGroup(ctx, "shared-qwen", remotes.GroupUpdate{Policy: &rr})
		Expect(err).NotTo(HaveOccurred())
		cfg := e.config("shared-qwen")
		Expect(cfg.Failover.Trip.Errors).To(Equal(3))
		Expect(cfg.Description).To(Equal("kept"))
	})

	It("refuses to drain the last model that takes requests", func() {
		create("qwen-local", "qwen")
		_, err := fed.Drain(ctx, "shared-qwen", "qwen-local", true)
		Expect(err).NotTo(HaveOccurred())
		_, err = fed.Drain(ctx, "shared-qwen", "qwen", true)
		Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
		g, err := fed.Drain(ctx, "shared-qwen", "qwen-local", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.Members[0].Drained).To(BeFalse())
	})

	DescribeTable("refuses a group that cannot work",
		func(name string, policy config.FailoverPolicy, members []string) {
			_, err := fed.CreateGroup(ctx, remotes.GroupRequest{Name: name, Policy: policy, Members: members})
			Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue(), "%v", err)
		},
		Entry("one member", "g", config.FailoverPolicy(""), []string{"qwen-local"}),
		Entry("the same member twice", "g", config.FailoverPolicy(""), []string{"qwen", "qwen"}),
		Entry("an unknown model", "g", config.FailoverPolicy(""), []string{"qwen", "nope"}),
		Entry("the priority policy", "g", config.FailoverPolicyPriority, []string{"qwen", "qwen-local"}),
		Entry("no name", "", config.FailoverPolicy(""), []string{"qwen", "qwen-local"}),
		Entry("a name that is not a file name", "a/b", config.FailoverPolicy(""), []string{"qwen", "qwen-local"}),
		Entry("itself as a member", "g", config.FailoverPolicy(""), []string{"g", "qwen", "qwen-local"}),
	)

	It("refuses a name that a model has", func() {
		_, err := fed.CreateGroup(ctx, remotes.GroupRequest{Name: "qwen-local", Members: []string{"qwen", "qwen-local"}})
		Expect(errors.Is(err, remotes.ErrConflict) || errors.Is(err, remotes.ErrInvalid)).To(BeTrue(), "%v", err)
	})

	It("does not nest groups", func() {
		create("qwen-local", "qwen")
		_, err := fed.CreateGroup(ctx, remotes.GroupRequest{Name: "outer", Members: []string{"shared-qwen", "qwen"}})
		Expect(errors.Is(err, remotes.ErrInvalid)).To(BeTrue())
	})

	It("deletes a group and leaves its models alone", func() {
		create("qwen-local", "qwen")
		Expect(fed.DeleteGroup(ctx, "shared-qwen")).To(Succeed())
		_, ok := e.loader.GetModelConfig("shared-qwen")
		Expect(ok).To(BeFalse())
		e.config("qwen-local")
		e.config("qwen")
		Expect(e.bus.ops("shared-qwen")).To(Equal([]string{"install", "delete"}))
	})

	It("does not treat a model or a priority chain as a group", func() {
		Expect(os.WriteFile(e.file("chain"), []byte("name: chain\nfailover:\n  targets:\n    - model: qwen\n    - model: qwen-local\n"), 0o600)).To(Succeed())
		Expect(e.loader.LoadModelConfigsFromPath(e.dir)).To(Succeed())
		for _, name := range []string{"chain", "qwen-local", "missing"} {
			_, err := fed.Group(ctx, name)
			Expect(errors.Is(err, remotes.ErrGroupNotFound)).To(BeTrue(), name)
			Expect(errors.Is(fed.DeleteGroup(ctx, name), remotes.ErrGroupNotFound)).To(BeTrue(), name)
		}
		v, err := fed.View(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Groups).To(BeEmpty())
	})
})
