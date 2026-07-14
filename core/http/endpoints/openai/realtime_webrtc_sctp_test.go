package openai

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("raiseDataChannelMaxMessageSize", func() {
	It("raises a max-message-size the browser advertised", func() {
		offer := "v=0\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\na=max-message-size:262144\r\n"
		out := raiseDataChannelMaxMessageSize(offer)
		Expect(out).To(ContainSubstring(fmt.Sprintf("a=max-message-size:%d", realtimeDataChannelMaxMessageSize)))
		Expect(out).NotTo(ContainSubstring("a=max-message-size:262144"))
	})

	It("leaves an offer without the attribute unchanged", func() {
		offer := "v=0\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n"
		Expect(raiseDataChannelMaxMessageSize(offer)).To(Equal(offer))
	})

	It("rewrites every occurrence", func() {
		offer := "a=max-message-size:1024\r\na=max-message-size:262144\r\n"
		out := raiseDataChannelMaxMessageSize(offer)
		Expect(strings.Count(out, fmt.Sprintf("a=max-message-size:%d", realtimeDataChannelMaxMessageSize))).To(Equal(2))
	})

	It("raises above the 256 KiB browsers advertise", func() {
		Expect(realtimeDataChannelMaxMessageSize).To(BeNumerically(">", 262144))
	})
})
