package carrier_test

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/nodes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The window of a change of carrier", func() {
	var (
		old, next *fakeCarrier
		cur       atomic.Pointer[carrier.Set]
		attached  map[string]carrier.Attachment
		agentsOn  map[cluster.Carrier]bool
		window    *carrier.Window
	)
	ctx := context.Background()

	BeforeEach(func() {
		old = newFakeCarrier(cluster.CarrierNATS, 1)
		next = newFakeCarrier(cluster.CarrierTunnel, 2)
		cur = atomic.Pointer[carrier.Set]{}
		cur.Store(next.set)
		attached = map[string]carrier.Attachment{}
		agentsOn = map[cluster.Carrier]bool{}
		window = carrier.NewWindow(
			func(_ context.Context, nodeID string) (carrier.Attachment, error) {
				if nodeID == "unknown" {
					return carrier.Attachment{}, errors.New("the registry is down")
				}
				return attached[nodeID], nil
			},
			func(_ context.Context, c cluster.Carrier) (bool, error) { return agentsOn[c], nil },
		)
	})

	Describe("when it is closed", func() {
		It("sends every call to the active set, without asking where the worker is", func() {
			asked := false
			closed := carrier.NewWindow(func(context.Context, string) (carrier.Attachment, error) {
				asked = true
				return carrier.Attachment{}, nil
			}, nil)
			files := carrier.NewFiles(&cur)
			files.UseWindow(closed)
			_, err := files.EnsureRemote(ctx, "n", "p", "k")
			Expect(err).ToNot(HaveOccurred())
			Expect(next.files.seen()).To(Equal([]string{"EnsureRemote"}))
			Expect(asked).To(BeFalse())
		})
	})

	Describe("when it is open", func() {
		BeforeEach(func() { window.Open(old.set) })

		It("sends file transfers to the carrier of the worker", func() {
			attached["a"] = carrier.Attachment{NATS: true}
			attached["b"] = carrier.Attachment{Tunnel: true}
			files := carrier.NewFiles(&cur)
			files.UseWindow(window)
			_, err := files.EnsureRemote(ctx, "a", "p", "k")
			Expect(err).ToNot(HaveOccurred())
			_, err = files.EnsureRemote(ctx, "b", "p", "k")
			Expect(err).ToNot(HaveOccurred())
			Expect(old.files.seen()).To(HaveLen(1))
			Expect(next.files.seen()).To(HaveLen(1))

			_, err = files.EnsureRemote(ctx, "nowhere", "p", "k")
			Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeTrue())
		})

		It("builds the client of a worker on the carrier of the worker", func() {
			attached["a"] = carrier.Attachment{NATS: true}
			clients := carrier.NewClients(&cur)
			clients.UseWindow(window)
			clients.NewClient("a", "addr", false)
			clients.NewClient("b", "addr", false)
			Expect(old.clients.seen()).To(HaveLen(1))
			Expect(next.clients.seen()).To(HaveLen(1), "a worker that is attached to neither gets the active carrier, whose first call says there is no route")
		})

		It("dials a worker through the carrier of the worker, and refuses one that is on neither", func() {
			attached["a"] = carrier.Attachment{NATS: true}
			dial := carrier.NewRoutedWorkerDialer(&cur, window)
			_, _ = dial("a")(ctx, "tcp", "x:1")
			Expect(old.dialed.seen()).To(Equal([]string{"dial:a"}))
			_, err := dial("nowhere")(ctx, "tcp", "x:1")
			Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeTrue())
		})

		It("uses the active carrier when it cannot tell where a worker is, and leaves the verdict to the carrier", func() {
			cmds := carrier.NewCommands(&cur)
			cmds.UseWindow(window)
			Expect(cmds.PingNode("unknown")).To(Succeed())
			Expect(next.commands.seen()).To(Equal([]string{"PingNode"}))
			Expect(old.commands.seen()).To(BeEmpty())
		})

		It("sends an agent request to the carrier that has agent workers", func() {
			agents := carrier.NewAgents(&cur)
			agents.UseWindow(window)

			agentsOn[cluster.CarrierTunnel] = true
			_, err := agents.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(next.agents.seen()).To(HaveLen(1))

			agentsOn[cluster.CarrierTunnel] = false
			agentsOn[cluster.CarrierNATS] = true
			_, err = agents.DiscoverMCPTools(ctx, mcpRemote.MCPDiscoveryRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(old.agents.seen()).To(HaveLen(1), "no agent worker has followed yet, so the old carrier still has them")

			agentsOn[cluster.CarrierNATS] = false
			_, err = agents.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(next.agents.seen()).To(HaveLen(2), "with none on either, the active carrier gives the error")
		})

		It("unloads a model through the previous carrier only for the nodes the active one could not reach", func() {
			cmds := carrier.NewCommands(&cur)
			cmds.UseWindow(window)
			Expect(cmds.UnloadRemoteModel("m")).To(Succeed())
			Expect(next.commands.seen()).To(Equal([]string{"UnloadRemoteModel"}))
			Expect(old.commands.seen()).To(BeEmpty(), "the active carrier did everything, so the previous one is not asked")
		})

		It("asks both carriers to delete the files of a model", func() {
			cmds := carrier.NewCommands(&cur)
			cmds.UseWindow(window)
			Expect(cmds.DeleteModelFiles("m")).To(Succeed())
			Expect(next.commands.seen()).To(Equal([]string{"DeleteModelFiles"}))
			Expect(old.commands.seen()).To(Equal([]string{"DeleteModelFiles"}))
		})

		It("goes back to the active set alone when it closes", func() {
			attached["a"] = carrier.Attachment{NATS: true}
			cmds := carrier.NewCommands(&cur)
			cmds.UseWindow(window)
			window.Close()
			Expect(cmds.PingNode("a")).To(Succeed())
			Expect(old.commands.seen()).To(BeEmpty())
			Expect(next.commands.seen()).To(HaveLen(1))
		})
	})
})
