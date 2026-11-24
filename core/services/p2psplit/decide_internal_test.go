package p2psplit

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

var (
	publicRelay = p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitBytes: 128 << 10, LimitDuration: 2 * time.Minute}
	ownRelay    = p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitBytes: 1 << 30, LimitDuration: 30 * time.Minute}
	tinyRelay   = p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitBytes: 64 << 10}
)

var _ = DescribeTable("the slow-path policy",
	func(connected bool, refusal string, path p2pnet.Path, own, allow bool, want State) {
		Expect(decide(connected, refusal, path, own, allow)).To(Equal(want))
	},
	Entry("no session", false, "", p2pnet.Path{Kind: p2pnet.PathNone}, false, false, StateOffline),
	Entry("a refused worker", true, "wrong engine", p2pnet.Path{Kind: p2pnet.PathDirect}, false, true, StateRefused),
	Entry("a direct path", true, "", p2pnet.Path{Kind: p2pnet.PathDirect}, false, false, StateOnline),
	Entry("a hole-punched path", true, "", p2pnet.Path{Kind: p2pnet.PathHolePunched}, false, false, StateOnline),
	Entry("a relay without limits, no choice yet", true, "", p2pnet.Path{Kind: p2pnet.PathRelayed}, false, false, StateSlow),
	Entry("a relay without limits, Use anyway", true, "", p2pnet.Path{Kind: p2pnet.PathRelayed}, false, true, StateOnline),
	Entry("a public relay with a data limit, even with Use anyway", true, "", publicRelay, false, true, StateLimited),
	Entry("a relay of the user with a 1 GiB circuit limit, no choice yet", true, "", ownRelay, true, false, StateSlow),
	Entry("a relay of the user with a 1 GiB circuit limit, Use anyway", true, "", ownRelay, true, true, StateOnline),
	Entry("a relay of the user with limits as small as a public relay's", true, "", tinyRelay, true, true, StateLimited),
	Entry("a relay with only a time limit, Use anyway", true, "",
		p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitDuration: time.Hour}, false, true, StateOnline),
)

var _ = Describe("the need of model traffic", func() {
	It("lets Use anyway through a relay of the user, never through a public relay", func() {
		Expect(needFor(false, false).SatisfiedBy(ownRelay)).To(BeFalse())
		Expect(needFor(true, true).SatisfiedBy(ownRelay)).To(BeTrue())
		Expect(needFor(true, true).SatisfiedBy(publicRelay)).To(BeFalse())
		Expect(needFor(true, false).SatisfiedBy(ownRelay)).To(BeFalse())
	})
})
