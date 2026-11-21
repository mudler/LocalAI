package carrier_test

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// holdersCommands is a carrier member that fans a model verb out over the nodes
// that hold the model, as the real sender does. A node that the carrier cannot
// reach fails with ErrNoRoute and keeps its row, as it does on a real carrier.
type holdersCommands struct {
	*fakeCommands
	holders []string
	reaches func(nodeID string) bool
}

var (
	_ nodes.NodeControl  = (*holdersCommands)(nil)
	_ nodes.ScopedFanout = (*holdersCommands)(nil)
)

func (f *holdersCommands) fan(verb string, include func(string) bool) error {
	var err error
	for _, id := range f.holders {
		if include != nil && !include(id) {
			continue
		}
		f.rec(verb + ":" + id)
		if !f.reaches(id) {
			err = fmt.Errorf("%w: node %s", nodes.ErrNoRoute, id)
		}
	}
	return err
}

func (f *holdersCommands) UnloadRemoteModel(string) error { return f.fan("unload", nil) }
func (f *holdersCommands) UnloadRemoteModelContext(context.Context, string, bool) error {
	return f.fan("unload", nil)
}
func (f *holdersCommands) DeleteModelFiles(string) error { return f.fan("delete", nil) }
func (f *holdersCommands) UnloadRemoteModelScoped(_ context.Context, _ string, _ bool, include func(string) bool) error {
	return f.fan("unload", include)
}
func (f *holdersCommands) DeleteModelFilesScoped(_ string, include func(string) bool) error {
	return f.fan("delete", include)
}

var _ = Describe("Model fan-out on a cluster that runs on NATS", func() {
	var (
		natsSet, routes *fakeCarrier
		cur             atomic.Pointer[carrier.Set]
		attached        map[string]carrier.Attachment
		commands        *carrier.Commands
	)

	BeforeEach(func() {
		natsSet = newFakeCarrier(cluster.CarrierNATS, 1)
		routes = newFakeCarrier(cluster.CarrierTunnel, 1)
		attached = map[string]carrier.Attachment{
			"on-nats":     {NATS: true},
			"tunnel-only": {Tunnel: true},
		}
		holders := []string{"on-nats", "tunnel-only"}
		natsSet.set.Commands = &holdersCommands{fakeCommands: natsSet.commands, holders: holders,
			reaches: func(id string) bool { return attached[id].NATS }}
		routes.set.Commands = &holdersCommands{fakeCommands: routes.commands, holders: holders,
			reaches: func(id string) bool { return attached[id].Tunnel }}
		cur = atomic.Pointer[carrier.Set]{}
		cur.Store(natsSet.set)
		window := carrier.NewWindow(func(_ context.Context, id string) (carrier.Attachment, error) {
			return attached[id], nil
		}, nil)
		window.UseTunnelRoutes(routes.set)
		commands = carrier.NewCommands(&cur)
		commands.UseWindow(window)
	})

	It("unloads a model on a tunnel-only worker through the tunnel and not through NATS", func() {
		Expect(commands.UnloadRemoteModelContext(context.Background(), "m", true)).To(Succeed())
		Expect(natsSet.commands.seen()).To(Equal([]string{"unload:on-nats"}))
		Expect(routes.commands.seen()).To(Equal([]string{"unload:tunnel-only"}))
	})

	It("does the same for the form of the unload that has no context", func() {
		Expect(commands.UnloadRemoteModel("m")).To(Succeed())
		Expect(natsSet.commands.seen()).To(Equal([]string{"unload:on-nats"}))
		Expect(routes.commands.seen()).To(Equal([]string{"unload:tunnel-only"}))
	})

	It("deletes the files of a model on a tunnel-only worker through the tunnel and not through NATS", func() {
		Expect(commands.DeleteModelFiles("m")).To(Succeed())
		Expect(natsSet.commands.seen()).To(Equal([]string{"delete:on-nats"}))
		Expect(routes.commands.seen()).To(Equal([]string{"delete:tunnel-only"}))
	})

	It("asks NATS for a worker that is attached to both", func() {
		attached["tunnel-only"] = carrier.Attachment{NATS: true, Tunnel: true}
		Expect(commands.UnloadRemoteModelContext(context.Background(), "m", false)).To(Succeed())
		Expect(natsSet.commands.seen()).To(Equal([]string{"unload:on-nats", "unload:tunnel-only"}))
		Expect(routes.commands.seen()).To(BeEmpty())
	})
})
