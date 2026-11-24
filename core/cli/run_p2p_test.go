package cli

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("P2P mode at start", func() {
	It("refuses share while authentication is off", func() {
		r := &RunCMD{P2PMode: "share", DataPath: "/data"}
		Expect(r.resolveP2PMode()).To(MatchError(config.ErrP2PShareNeedsAuth))
	})

	It("refuses connect without a data path, because the identity cannot be kept", func() {
		r := &RunCMD{P2PMode: "connect", DataPath: NoDataPath}
		r.normalizeDataPath()
		Expect(r.resolveP2PMode()).To(MatchError(config.ErrP2PNoDataPath))
	})

	It("accepts connect without a data path when the identity file is named", func() {
		r := &RunCMD{P2PMode: "connect", P2PIdentity: "/keys/identity.key"}
		Expect(r.resolveP2PMode()).To(Succeed())
	})

	It("needs nothing when P2P is off", func() {
		r := &RunCMD{P2PMode: "off"}
		Expect(r.resolveP2PMode()).To(Succeed())
	})

	It("refuses a bootstrap peer without a peer ID", func() {
		r := &RunCMD{P2PMode: "connect", DataPath: "/data", P2PBootstrap: []string{"/ip4/192.0.2.1/tcp/4001"}}
		Expect(r.resolveP2PMode()).To(MatchError(ContainSubstring("--p2p-bootstrap")))
	})
})
