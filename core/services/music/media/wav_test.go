// SPDX-License-Identifier: MIT
package media_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/mudler/LocalAI/core/services/music/media"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"math"
	"os"
	"runtime"
)

func wav(tag, bits, ch uint16, rate uint32, frames int) []byte {
	n := frames * int(ch) * int(bits) / 8
	b := make([]byte, 44+n+n%2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], tag)
	binary.LittleEndian.PutUint16(b[22:], ch)
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*uint32(ch)*uint32(bits)/8)
	binary.LittleEndian.PutUint16(b[32:], ch*bits/8)
	binary.LittleEndian.PutUint16(b[34:], bits)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n))
	return b
}

var _ = Describe("strict WAV boundary", func() {
	It("returns exact metadata and restores the original position", func() {
		b := wav(1, 24, 2, 48000, 4800)
		r := bytes.NewReader(b)
		_, e := r.Seek(7, io.SeekStart)
		Expect(e).NotTo(HaveOccurred())
		m, e := media.ValidateRecordingWAV(context.Background(), r, int64(len(b)))
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Encoding).To(Equal(media.PCM))
		Expect(m.Frames).To(Equal(uint64(4800)))
		Expect(m.InterleavedSamples).To(Equal(uint64(9600)))
		Expect(m.BitsPerSample).To(Equal(uint16(24)))
		pos, e := r.Seek(0, io.SeekCurrent)
		Expect(e).NotTo(HaveOccurred())
		Expect(pos).To(Equal(int64(7)))
	})
	It("rejects truncated payload instead of trusting metadata", func() {
		b := wav(1, 16, 1, 8000, 800)
		_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b[:len(b)-1]), int64(len(b)-1))
		Expect(e).To(HaveOccurred())
	})
	It("rejects floating input and nonfinite output", func() {
		b := wav(3, 32, 1, 8000, 800)
		binary.LittleEndian.PutUint32(b[44:], math.Float32bits(float32(math.Inf(1))))
		_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		Expect(e).To(HaveOccurred())
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(HaveOccurred())
	})
	It("enforces recording duration and rejects empty output", func() {
		b := wav(1, 16, 1, 8000, 799)
		_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		Expect(e).To(HaveOccurred())
		b = wav(1, 16, 1, 8000, 0)
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(HaveOccurred())
	})
	It("honors cancellation before reading", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		b := wav(1, 16, 1, 8000, 800)
		_, e := media.ValidateRecordingWAV(ctx, bytes.NewReader(b), int64(len(b)))
		Expect(e).To(MatchError(context.Canceled))
	})
	DescribeTable("accepts every approved integer PCM width", func(bits uint16) {
		b := wav(1, bits, 2, 8000, 800)
		m, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		Expect(e).NotTo(HaveOccurred())
		Expect(m.BitsPerSample).To(Equal(bits))
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(Succeed())
	}, Entry("unsigned8", uint16(8)), Entry("signed16", uint16(16)), Entry("signed24", uint16(24)), Entry("signed32", uint16(32)))
	DescribeTable("accepts finite output floats and rejects nonfinite", func(bits uint16, value float64) {
		b := wav(3, bits, 1, 8000, 1)
		if bits == 32 {
			binary.LittleEndian.PutUint32(b[44:], math.Float32bits(float32(value)))
		} else {
			binary.LittleEndian.PutUint64(b[44:], math.Float64bits(value))
		}
		e := media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		if math.IsInf(value, 0) || math.IsNaN(value) {
			Expect(e).To(HaveOccurred())
		} else {
			Expect(e).NotTo(HaveOccurred())
		}
	}, Entry("float32", uint16(32), 0.5), Entry("float64", uint16(64), -0.25), Entry("NaN32", uint16(32), math.NaN()), Entry("NaN64", uint16(64), math.NaN()), Entry("negativeInf", uint16(64), math.Inf(-1)))
	DescribeTable("rejects malformed and unsupported containers", func(change func([]byte) []byte) {
		b := change(wav(1, 16, 1, 8000, 800))
		_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		Expect(e).To(HaveOccurred())
	},
		Entry("RIFX", func(b []byte) []byte { copy(b, "RIFX"); return b }), Entry("RF64", func(b []byte) []byte { copy(b, "RF64"); return b }), Entry("notWAVE", func(b []byte) []byte { copy(b[8:], "AVI "); return b }), Entry("extensible", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:], 0xfffe); return b }), Entry("compressed", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[20:], 6); return b }), Entry("badRIFFsize", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:], 1); return b }), Entry("hugechunk", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:], 0xffffffff); return b }), Entry("unaligned", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:], 1599); return b }), Entry("badblock", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[32:], 1); return b }), Entry("badbyterate", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[28:], 1); return b }), Entry("threechannels", func(b []byte) []byte { binary.LittleEndian.PutUint16(b[22:], 3); return b }), Entry("rateover", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[24:], 48001); return b }), Entry("datafirst", func(b []byte) []byte { copy(b[12:], "data"); return b }), Entry("trailingbyte", func(b []byte) []byte { return append(b, 0) }))
	It("validates odd chunk padding and duplicate chunks", func() {
		base := wav(1, 8, 1, 8000, 801)
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(base), int64(len(base)))).To(Succeed())
		base[len(base)-1] = 1
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(base), int64(len(base)))).To(HaveOccurred())
		for _, chunk := range [][]byte{wav(1, 16, 1, 8000, 800)[12:36], wav(1, 16, 1, 8000, 800)[36:]} {
			b := append(wav(1, 16, 1, 8000, 800), chunk...)
			binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
			Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(HaveOccurred())
		}
	})
	It("checks both exact duration endpoints and preserves cursor on failure", func() {
		for _, frames := range []int{800, 480000} {
			b := wav(1, 8, 1, 8000, frames)
			_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
			Expect(e).NotTo(HaveOccurred())
		}
		b := wav(1, 8, 1, 8000, 480001)
		r := bytes.NewReader(b)
		_, e := r.Seek(9, io.SeekStart)
		Expect(e).NotTo(HaveOccurred())
		_, e = media.ValidateRecordingWAV(context.Background(), r, int64(len(b)))
		Expect(e).To(HaveOccurred())
		p, e := r.Seek(0, io.SeekCurrent)
		Expect(e).NotTo(HaveOccurred())
		Expect(p).To(Equal(int64(9)))
	})
	It("checks the exact output 300 second endpoint", func() {
		b := wav(1, 8, 1, 8000, 2400000)
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(Succeed())
		b = wav(1, 8, 1, 8000, 2400001)
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(HaveOccurred())
	})
	It("rejects wrong actual size and encoded ceilings", func() {
		b := wav(1, 16, 1, 8000, 800)
		_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)+1))
		Expect(e).To(HaveOccurred())
		_, e = media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), 25<<20+1)
		Expect(e).To(HaveOccurred())
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), 128<<20+1)).To(HaveOccurred())
	})
	It("handles short reads and cancellation during payload", func() {
		b := wav(1, 24, 2, 48000, 4800)
		r := &shortReader{Reader: bytes.NewReader(b)}
		_, e := media.ValidateRecordingWAV(context.Background(), r, int64(len(b)))
		Expect(e).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		r = &shortReader{Reader: bytes.NewReader(b), cancel: cancel}
		_, e = media.ValidateRecordingWAV(ctx, r, int64(len(b)))
		Expect(e).To(MatchError(context.Canceled))
	})
	It("bounds chunk traversal and allocations for declared huge chunks", func() {
		b := wav(1, 16, 1, 8000, 800)
		binary.LittleEndian.PutUint32(b[40:], 0xffffffff)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		runtime.ReadMemStats(&after)
		Expect(e).To(HaveOccurred())
		Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 1<<20))
		b = wav(1, 16, 1, 8000, 800)
		for i := 0; i < 1024; i++ {
			b = append(b, []byte("JUNK\x00\x00\x00\x00")...)
		}
		binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(HaveOccurred())
	})
	It("returns sanitized structural errors", func() {
		b := wav(1, 16, 1, 8000, 0)
		e := media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		var public interface {
			HTTPStatus() int
			MusicStage() string
			MusicReason() string
		}
		Expect(errors.As(e, &public)).To(BeTrue())
		Expect(public.HTTPStatus()).To(Equal(422))
		Expect(public.MusicStage()).To(Equal("validation"))
		Expect(public.MusicReason()).To(Equal("invalid_input"))
	})

	It("accepts bounded ancillary chunks including odd padding", func() {
		base := wav(1, 16, 1, 8000, 800)
		b := append([]byte{}, base[:36]...)
		b = append(b, []byte("JUNK\x03\x00\x00\x00abc\x00")...)
		b = append(b, base[36:]...)
		binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
		_, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		Expect(e).NotTo(HaveOccurred())
	})
	It("accepts the maximum recording sample count without whole-file buffering", func() {
		b := wav(1, 8, 2, 48000, 2880000)
		m, e := media.ValidateRecordingWAV(context.Background(), bytes.NewReader(b), int64(len(b)))
		Expect(e).NotTo(HaveOccurred())
		Expect(m.InterleavedSamples).To(Equal(uint64(5760000)))
	})
	It("rejects absent mandatory chunks and truncated chunk headers", func() {
		b := wav(1, 16, 1, 8000, 800)[:36]
		binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(HaveOccurred())
		b = append(wav(1, 16, 1, 8000, 800), 1, 2, 3)
		binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
		Expect(media.ValidateOutputWAV(context.Background(), bytes.NewReader(b), int64(len(b)))).To(HaveOccurred())
	})
	It("restores the reader cursor on payload cancellation", func() {
		b := wav(1, 16, 1, 8000, 800)
		ctx, cancel := context.WithCancel(context.Background())
		r := &shortReader{Reader: bytes.NewReader(b), cancel: cancel}
		_, e := r.Seek(11, io.SeekStart)
		Expect(e).NotTo(HaveOccurred())
		_, e = media.ValidateRecordingWAV(ctx, r, int64(len(b)))
		Expect(e).To(MatchError(context.Canceled))
		p, e := r.Seek(0, io.SeekCurrent)
		Expect(e).NotTo(HaveOccurred())
		Expect(p).To(Equal(int64(11)))
	})
	It("validates the independently supplied real 20-second WAV (format evidence only)", func() {
		path := os.Getenv("LOCALAI_MUSIC_TEST_WAV")
		if path == "" {
			Skip("set LOCALAI_MUSIC_TEST_WAV to the reviewed 20-second fixture")
		}
		f, e := os.Open(path)
		Expect(e).NotTo(HaveOccurred())
		defer f.Close()
		st, e := f.Stat()
		Expect(e).NotTo(HaveOccurred())
		m, e := media.ValidateRecordingWAV(context.Background(), f, st.Size())
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Frames).To(Equal(uint64(m.SampleRate) * 20))
		Expect(media.ValidateOutputWAV(context.Background(), f, st.Size())).To(Succeed())
	})

})

type shortReader struct {
	*bytes.Reader
	cancel context.CancelFunc
	total  int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if len(p) > 7 {
		p = p[:7]
	}
	n, e := r.Reader.Read(p)
	r.total += n
	if r.cancel != nil && r.total > 100 {
		r.cancel()
	}
	return n, e
}
