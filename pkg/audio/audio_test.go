package audio

import (
	"bytes"
	"encoding/binary"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WAV utilities", func() {
	Describe("NewWAVHeader", func() {
		It("produces a valid 44-byte header", func() {
			hdr := NewWAVHeader(3200)
			var buf bytes.Buffer
			Expect(hdr.Write(&buf)).To(Succeed())
			Expect(buf.Len()).To(Equal(WAVHeaderSize))

			b := buf.Bytes()
			Expect(string(b[0:4])).To(Equal("RIFF"))
			Expect(string(b[8:12])).To(Equal("WAVE"))
			Expect(string(b[12:16])).To(Equal("fmt "))

			Expect(binary.LittleEndian.Uint16(b[20:22])).To(Equal(uint16(1))) // PCM
			Expect(binary.LittleEndian.Uint16(b[22:24])).To(Equal(uint16(1))) // mono
			Expect(binary.LittleEndian.Uint32(b[24:28])).To(Equal(uint32(16000)))
			Expect(binary.LittleEndian.Uint32(b[28:32])).To(Equal(uint32(32000)))
			Expect(string(b[36:40])).To(Equal("data"))
			Expect(binary.LittleEndian.Uint32(b[40:44])).To(Equal(uint32(3200)))
		})
	})

	Describe("NewWAVHeaderWithRate", func() {
		It("uses the custom sample rate", func() {
			hdr := NewWAVHeaderWithRate(4800, 24000)
			var buf bytes.Buffer
			Expect(hdr.Write(&buf)).To(Succeed())
			b := buf.Bytes()

			Expect(binary.LittleEndian.Uint32(b[24:28])).To(Equal(uint32(24000)))
			Expect(binary.LittleEndian.Uint32(b[28:32])).To(Equal(uint32(48000)))
		})
	})

	Describe("StripWAVHeader", func() {
		It("strips the 44-byte header", func() {
			pcm := []byte{0xDE, 0xAD, 0xBE, 0xEF}
			hdr := NewWAVHeader(uint32(len(pcm)))
			var buf bytes.Buffer
			Expect(hdr.Write(&buf)).To(Succeed())
			buf.Write(pcm)

			got := StripWAVHeader(buf.Bytes())
			Expect(got).To(Equal(pcm))
		})

		It("returns short data unchanged", func() {
			short := []byte{0x01, 0x02, 0x03}
			Expect(StripWAVHeader(short)).To(Equal(short))

			exact := make([]byte, WAVHeaderSize)
			Expect(StripWAVHeader(exact)).To(Equal(exact))
		})
	})

	Describe("ParseWAV", func() {
		It("returns sample rate and PCM data", func() {
			pcm := make([]byte, 100)
			for i := range pcm {
				pcm[i] = byte(i)
			}

			hdr24 := NewWAVHeaderWithRate(uint32(len(pcm)), 24000)
			var buf24 bytes.Buffer
			hdr24.Write(&buf24)
			buf24.Write(pcm)

			gotPCM, gotRate := ParseWAV(buf24.Bytes())
			Expect(gotRate).To(Equal(24000))
			Expect(gotPCM).To(Equal(pcm))

			hdr16 := NewWAVHeader(uint32(len(pcm)))
			var buf16 bytes.Buffer
			hdr16.Write(&buf16)
			buf16.Write(pcm)

			gotPCM, gotRate = ParseWAV(buf16.Bytes())
			Expect(gotRate).To(Equal(16000))
			Expect(gotPCM).To(Equal(pcm))
		})

		It("returns zero rate for short data", func() {
			short := []byte{0x01, 0x02, 0x03}
			gotPCM, gotRate := ParseWAV(short)
			Expect(gotRate).To(Equal(0))
			Expect(gotPCM).To(Equal(short))
		})
	})
})
