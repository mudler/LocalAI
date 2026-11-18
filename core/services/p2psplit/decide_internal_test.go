package p2psplit

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

var _ = DescribeTable("the slow-path policy",
	func(connected bool, refusal string, path p2pnet.Path, allow bool, want State) {
		Expect(decide(connected, refusal, path, allow)).To(Equal(want))
	},
	Entry("no session", false, "", p2pnet.Path{Kind: p2pnet.PathNone}, false, StateOffline),
	Entry("a refused worker", true, "wrong engine", p2pnet.Path{Kind: p2pnet.PathDirect}, true, StateRefused),
	Entry("a direct path", true, "", p2pnet.Path{Kind: p2pnet.PathDirect}, false, StateOnline),
	Entry("a hole-punched path", true, "", p2pnet.Path{Kind: p2pnet.PathHolePunched}, false, StateOnline),
	Entry("a relay without limits, no choice yet", true, "", p2pnet.Path{Kind: p2pnet.PathRelayed}, false, StateSlow),
	Entry("a relay without limits, Use anyway", true, "", p2pnet.Path{Kind: p2pnet.PathRelayed}, true, StateOnline),
	Entry("a public relay with a data limit, even with Use anyway", true, "",
		p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitBytes: 128 << 10, LimitDuration: 2 * time.Minute}, true, StateLimited),
	Entry("a user relay with a 1 GiB circuit limit", true, "",
		p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitBytes: 1 << 30, LimitDuration: 30 * time.Minute}, true, StateLimited),
	Entry("a relay with only a time limit, Use anyway", true, "",
		p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitDuration: time.Hour}, true, StateOnline),
)
