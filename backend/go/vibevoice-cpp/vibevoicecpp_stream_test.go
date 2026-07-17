package main

import (
	"encoding/binary"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TTSStream callback framing", func() {
	// The real C callback copies a transient int16 PCM buffer out of the
	// engine into a fresh []byte and pushes it onto the active stream's
	// results channel as little-endian bytes. deliverPCMForTest runs that
	// exact copy-and-push path against a []int16 so we can validate the
	// framing without the C library (full audio e2e is a later task).
	It("copies int16 PCM from the C callback into the results channel as LE bytes", func() {
		samples := []int16{0, 1, -1, 32767, -32768, 1234, -4321}

		prev := activeStream
		DeferCleanup(func() { activeStream = prev })

		s := &streamState{results: make(chan []byte, 1)}
		activeStream = s

		deliverPCMForTest(samples)

		var got []byte
		Eventually(s.results).Should(Receive(&got))
		Expect(got).To(HaveLen(len(samples) * 2))

		want := make([]byte, len(samples)*2)
		for i, v := range samples {
			binary.LittleEndian.PutUint16(want[i*2:], uint16(v))
		}
		Expect(got).To(Equal(want))
	})

	It("is a no-op when there is no active stream", func() {
		prev := activeStream
		DeferCleanup(func() { activeStream = prev })
		activeStream = nil

		// Must not panic when no stream is installed.
		Expect(func() { deliverPCMForTest([]int16{1, 2, 3}) }).ToNot(Panic())
	})
})
