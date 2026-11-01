package application

import (
	"net/netip"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

var _ = Describe("the server's own addresses for the Sandbox proxy", func() {
	It("sends web_fetch's list, external base URL included, with an allow and nothing with a block", func() {
		a := &Application{applicationConfig: &config.ApplicationConfig{ExternalBaseURL: "https://203.0.113.9:8443/base"}}
		self := a.serverSelf()
		Expect(self()).To(ContainElement(netip.MustParsePrefix("203.0.113.9/32")))

		allow := hostVerdict(egress.Verdict{Action: egress.Allow, Reason: egress.ReasonPreset}, self)
		Expect(allow.Self).To(ContainElements("203.0.113.9/32", "127.0.0.0/8", "::1/128"))
		Expect(safehttp.ParseDenyList(allow.Self)).To(ContainElement(netip.MustParsePrefix("203.0.113.9/32")))

		block := hostVerdict(egress.Verdict{Action: egress.Block, Reason: egress.ReasonOff}, self)
		Expect(block.Self).To(BeEmpty())
	})

	It("builds web_fetch on the same list", func() {
		a := &Application{applicationConfig: &config.ApplicationConfig{ExternalBaseURL: "https://203.0.113.10"}}
		w := newStudioWeb(a.applicationConfig, a.serverSelf())
		Expect(w.self()).To(ContainElement(netip.MustParsePrefix("203.0.113.10/32")))
	})
})
