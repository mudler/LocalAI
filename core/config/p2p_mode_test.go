package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResolveP2PMode", func() {
	DescribeTable("resolves the mode",
		func(requested string, auth bool, want P2PMode, wantErr bool, msgPart string) {
			got, msg, err := ResolveP2PMode(requested, auth)
			Expect(got).To(Equal(want))
			if wantErr {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal(msg))
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
			Expect(msg).To(ContainSubstring(msgPart))
		},
		Entry("empty is off", "", false, P2PModeOff, false, ""),
		Entry("off", "off", true, P2PModeOff, false, ""),
		Entry("connect without auth", "connect", false, P2PModeConnect, false, "accepts no one"),
		Entry("connect with auth", "connect", true, P2PModeConnect, false, "accepts no one"),
		Entry("share with auth", "share", true, P2PModeShare, false, "accepts paired devices"),
		Entry("share without auth is refused", "share", false, P2PModeOff, true, "needs authentication"),
		Entry("share refusal names the real flag", "share", false, P2PModeOff, true, "--auth-enabled"),
		Entry("case and spaces are ignored", " Share ", true, P2PModeShare, false, "share"),
		Entry("unknown value", "on", true, P2PModeOff, true, "off, connect or share"),
	)

	It("tells which modes dial and which accept", func() {
		Expect(P2PModeOff.Dials()).To(BeFalse())
		Expect(P2PModeConnect.Dials()).To(BeTrue())
		Expect(P2PModeConnect.Accepts()).To(BeFalse())
		Expect(P2PModeShare.Dials()).To(BeTrue())
		Expect(P2PModeShare.Accepts()).To(BeTrue())
	})
})
