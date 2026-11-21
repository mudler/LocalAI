package carrier_test

import (
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// closingBus counts Close calls on a FakeBus.
type closingBus struct {
	*testutil.FakeBus
	closed atomic.Int32
}

func (c *closingBus) Close() { c.closed.Add(1) }

// noModels is a ModelLocator that knows no node.
type noModels struct{ nodes.ModelLocator }

var _ = Describe("NewNATSRoutes", func() {
	var bus *closingBus

	BeforeEach(func() {
		bus = &closingBus{FakeBus: testutil.NewFakeBus()}
	})

	opts := func() carrier.NATSOptions {
		return carrier.NATSOptions{
			Client:         bus,
			Registry:       noModels{},
			InstallTimeout: 3 * time.Minute,
			UpgradeTimeout: 4 * time.Minute,
			Token:          "token",
			HTTPAddrFor:    func(string) (string, error) { return "127.0.0.1:1", nil },
		}
	}

	It("builds a complete direct route", func() {
		set, err := carrier.NewNATSRoutes(opts())
		Expect(err).ToNot(HaveOccurred())
		Expect(set.Validate()).To(Succeed())
		Expect(set.Kind).To(Equal(nodes.RouteDirect))
		Expect(set.Agents).ToNot(BeNil())
		Expect(set.Commands.InstallTimeout()).To(Equal(3 * time.Minute))
	})

	It("stages files over HTTP unless shared storage is configured", func() {
		set, err := carrier.NewNATSRoutes(opts())
		Expect(err).ToNot(HaveOccurred())
		Expect(set.Files).To(BeAssignableToTypeOf(&nodes.HTTPFileStager{}))

		o := opts()
		o.S3Staging = true
		set, err = carrier.NewNATSRoutes(o)
		Expect(err).ToNot(HaveOccurred())
		Expect(set.Files).To(BeAssignableToTypeOf(&nodes.S3FileStager{}))
	})

	It("closes the connection when the route is closed", func() {
		set, err := carrier.NewNATSRoutes(opts())
		Expect(err).ToNot(HaveOccurred())
		set.Close()
		Expect(bus.closed.Load()).To(Equal(int32(1)))
	})

	It("leaves a connection it shares with the NATS bus open when the route is closed", func() {
		o := opts()
		o.Shared = true
		set, err := carrier.NewNATSRoutes(o)
		Expect(err).ToNot(HaveOccurred())
		if set.Close != nil {
			set.Close()
		}
		Expect(bus.closed.Load()).To(BeZero())
	})

	It("refuses to build without a connection", func() {
		o := opts()
		o.Client = nil
		_, err := carrier.NewNATSRoutes(o)
		Expect(err).To(HaveOccurred())
	})
})
