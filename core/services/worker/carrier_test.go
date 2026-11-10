package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The carrier that a worker attaches to", func() {
	DescribeTable("follows the answer of the registration",
		func(named, natsURL string, wantTunnel bool, wantErr string) {
			onTunnel, err := carrierOf(named, "", &Config{NatsURL: natsURL})
			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
				return
			}
			Expect(err).ToNot(HaveOccurred())
			Expect(onTunnel).To(Equal(wantTunnel))
		},
		Entry("the tunnel, with no NATS URL", "tunnel", "", true, ""),
		Entry("the tunnel, with a NATS URL that is not used", "tunnel", "nats://n:4222", true, ""),
		Entry("NATS, with a URL", "nats", "nats://n:4222", false, ""),
		Entry("no name (a frontend before carriers), with a URL", "", "nats://n:4222", false, ""),
		Entry("NATS, with no URL", "nats", "", false, "no NATS URL"),
		Entry("no name, with no URL", "", "", false, "does not name a carrier"),
		Entry("NATS, with no URL of its own and none from the frontend", "nats", "", false, "handed over none"),
		Entry("a carrier from the future", "carrier-pigeon", "nats://n:4222", false, "does not know"),
	)

	DescribeTable("takes the address of NATS from the frontend when it has none",
		func(local, handed string, wantErr bool) {
			onTunnel, err := carrierOf("nats", handed, &Config{NatsURL: local})
			if wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).ToNot(HaveOccurred())
			Expect(onTunnel).To(BeFalse())
		},
		Entry("only the frontend gives one", "", "nats://frontend-gave:4222", false),
		Entry("only the worker has one", "nats://local:4222", "", false),
		Entry("both, and the local one wins as an override", "nats://local:4222", "nats://frontend-gave:4222", false),
		Entry("neither", "", "", true),
	)
})
