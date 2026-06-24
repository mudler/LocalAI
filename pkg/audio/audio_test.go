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

	Describe("non-canonical RIFF layouts", func() {
		// chunk builds a word-aligned RIFF sub-chunk (id + size + body + pad).
		chunk := func(id string, body []byte) []byte {
			out := append([]byte(id), 0, 0, 0, 0)
			binary.LittleEndian.PutUint32(out[4:8], uint32(len(body)))
			out = append(out, body...)
			if len(body)&1 == 1 {
				out = append(out, 0) // pad byte for odd-sized chunks
			}
			return out
		}
		// fmtBody is a mono 16-bit PCM `fmt ` body; extra simulates the
		// 18/40-byte extensible form (cbSize + extension).
		fmtBody := func(rate uint32, extra int) []byte {
			b := make([]byte, 16+extra)
			binary.LittleEndian.PutUint16(b[0:2], 1)       // PCM
			binary.LittleEndian.PutUint16(b[2:4], 1)       // mono
			binary.LittleEndian.PutUint32(b[4:8], rate)    // sample rate
			binary.LittleEndian.PutUint32(b[8:12], rate*2) // byte rate
			binary.LittleEndian.PutUint16(b[12:14], 2)     // block align
			binary.LittleEndian.PutUint16(b[14:16], 16)    // bits per sample
			if extra >= 2 {
				binary.LittleEndian.PutUint16(b[16:18], uint16(extra-2)) // cbSize
			}
			return b
		}
		riff := func(chunks ...[]byte) []byte {
			body := []byte("WAVE")
			for _, c := range chunks {
				body = append(body, c...)
			}
			out := append([]byte("RIFF"), 0, 0, 0, 0)
			binary.LittleEndian.PutUint32(out[4:8], uint32(len(body)))
			return append(out, body...)
		}
		pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}

		It("ignores JUNK/LIST chunks before data (no leading splice)", func() {
			w := riff(
				chunk("fmt ", fmtBody(16000, 0)),
				chunk("JUNK", []byte("padding-bytes-x")), // odd length → exercises pad
				chunk("LIST", []byte("INFOISFTLavf")),
				chunk("data", pcm),
			)
			gotPCM, rate := ParseWAV(w)
			Expect(rate).To(Equal(16000))
			Expect(gotPCM).To(Equal(pcm))
			Expect(StripWAVHeader(w)).To(Equal(pcm))
		})

		It("honours the data chunk size and drops a trailing chunk", func() {
			w := riff(
				chunk("fmt ", fmtBody(24000, 0)),
				chunk("data", pcm),
				chunk("LIST", []byte("INFOISFTLavf60.16")), // ffmpeg trailer tag
			)
			gotPCM, rate := ParseWAV(w)
			Expect(rate).To(Equal(24000))
			Expect(gotPCM).To(Equal(pcm)) // trailing LIST not spliced in
		})

		It("handles an 18-byte extensible fmt chunk", func() {
			w := riff(chunk("fmt ", fmtBody(16000, 2)), chunk("data", pcm))
			gotPCM, rate := ParseWAV(w)
			Expect(rate).To(Equal(16000))
			Expect(gotPCM).To(Equal(pcm))
		})

		It("returns non-WAV input unchanged", func() {
			raw := []byte("this is definitely not a riff wave file")
			gotPCM, rate := ParseWAV(raw)
			Expect(rate).To(Equal(0))
			Expect(gotPCM).To(Equal(raw))
			Expect(StripWAVHeader(raw)).To(Equal(raw))
		})
	})
})
