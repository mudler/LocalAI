package utils_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"

	laudio "github.com/mudler/LocalAI/pkg/audio"
	. "github.com/mudler/LocalAI/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// writeWAV writes a 16-bit PCM WAV with the given shape. The samples are a
// ramp rather than silence, so a conversion that dropped its input would be
// visible as a shorter file and not just as a different header.
func writeWAV(path string, sampleRate uint32, channels uint16, frames int) {
	f, err := os.Create(path)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = f.Close() }()

	const bitsPerSample = uint16(16)
	blockAlign := channels * (bitsPerSample / 8)
	total := frames * int(channels)
	dataSize := uint32(total) * uint32(bitsPerSample/8)

	hdr := laudio.WAVHeader{
		ChunkID:       [4]byte{'R', 'I', 'F', 'F'},
		ChunkSize:     36 + dataSize,
		Format:        [4]byte{'W', 'A', 'V', 'E'},
		Subchunk1ID:   [4]byte{'f', 'm', 't', ' '},
		Subchunk1Size: 16,
		AudioFormat:   1,
		NumChannels:   channels,
		SampleRate:    sampleRate,
		ByteRate:      sampleRate * uint32(blockAlign),
		BlockAlign:    blockAlign,
		BitsPerSample: bitsPerSample,
		Subchunk2ID:   [4]byte{'d', 'a', 't', 'a'},
		Subchunk2Size: dataSize,
	}
	Expect(binary.Write(f, binary.LittleEndian, &hdr)).To(Succeed())
	for i := 0; i < total; i++ {
		Expect(binary.Write(f, binary.LittleEndian, int16(200*(i%100)))).To(Succeed())
	}
}

// readShape reports the (rate, channels, bit depth) a WAV declares.
func readShape(path string) (uint32, uint16, uint16) {
	f, err := os.Open(path)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = f.Close() }()
	var hdr laudio.WAVHeader
	Expect(binary.Read(f, binary.LittleEndian, &hdr)).To(Succeed())
	return hdr.SampleRate, hdr.NumChannels, hdr.BitsPerSample
}

// readFormatTag reports wFormatTag, which sits at the fixed offset 20 of a
// RIFF/WAVE header. 1 is plain PCM; 0xFFFE is WAVE_FORMAT_EXTENSIBLE.
func readFormatTag(path string) uint16 {
	raw, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	Expect(len(raw)).To(BeNumerically(">=", 22))
	return binary.LittleEndian.Uint16(raw[20:22])
}

// writeExtensibleWAV writes a real WAVE_FORMAT_EXTENSIBLE file: a 40-byte fmt
// chunk with tag 0xFFFE, cbSize 22 and the KSDATAFORMAT_SUBTYPE_PCM GUID. Many
// DAWs and Windows tools emit exactly this, and the samples inside are ordinary
// 16-bit PCM, which is why a bit-depth-only check waves it through.
func writeExtensibleWAV(path string, sampleRate uint32, channels uint16, frames int) {
	const bitsPerSample = uint16(16)
	blockAlign := channels * (bitsPerSample / 8)
	dataSize := uint32(frames) * uint32(channels) * uint32(bitsPerSample/8)

	buf := &bytes.Buffer{}
	write := func(values ...any) {
		for _, value := range values {
			Expect(binary.Write(buf, binary.LittleEndian, value)).To(Succeed())
		}
	}
	buf.WriteString("RIFF")
	write(uint32(4 + 8 + 40 + 8 + dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	write(uint32(40))                      // fmt chunk size for extensible
	write(uint16(0xFFFE))                  // wFormatTag = WAVE_FORMAT_EXTENSIBLE
	write(channels, sampleRate)            // nChannels, nSamplesPerSec
	write(sampleRate * uint32(blockAlign)) // nAvgBytesPerSec
	write(blockAlign, bitsPerSample)       // nBlockAlign, wBitsPerSample
	write(uint16(22), bitsPerSample)       // cbSize, wValidBitsPerSample
	write(uint32(0x3))                     // dwChannelMask: front left + right
	// KSDATAFORMAT_SUBTYPE_PCM {00000001-0000-0010-8000-00AA00389B71}
	buf.Write([]byte{
		0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00,
		0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71,
	})
	buf.WriteString("data")
	write(dataSize)
	for i := 0; i < frames*int(channels); i++ {
		write(int16(200 * (i % 100)))
	}
	Expect(os.WriteFile(path, buf.Bytes(), 0o600)).To(Succeed())
}

// The distinction these specs defend: /audio/transform used to fold EVERY
// upload to 16 kHz mono, which made source separation impossible to reach
// through the HTTP API (htdemucs refuses any rate but its checkpoint's own and
// separates using the stereo image). AudioToWav keeps that fold for the
// backends that want it; AudioToWavPreservingShape is what everything else
// gets.
var _ = Describe("utils/ffmpeg shape-preserving conversion", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	It("passes a 44.1 kHz stereo WAV through byte for byte", func() {
		src := filepath.Join(dir, "input.wav")
		dst := filepath.Join(dir, "output.wav")
		writeWAV(src, 44100, 2, 4410)

		Expect(AudioToWavPreservingShape(src, dst)).To(Succeed())

		rate, channels, depth := readShape(dst)
		Expect(rate).To(Equal(uint32(44100)), "the source rate must survive")
		Expect(channels).To(Equal(uint16(2)), "the stereo image must survive")
		Expect(depth).To(Equal(uint16(16)))

		before, err := os.ReadFile(src)
		Expect(err).ToNot(HaveOccurred())
		after, err := os.ReadFile(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(after).To(Equal(before), "a 16-bit PCM WAV is not re-encoded at all")
	})

	It("keeps an unusual rate and channel count that AudioToWav would destroy", func() {
		src := filepath.Join(dir, "odd.wav")
		preserved := filepath.Join(dir, "preserved.wav")
		folded := filepath.Join(dir, "folded.wav")
		writeWAV(src, 48000, 2, 4800)

		Expect(AudioToWavPreservingShape(src, preserved)).To(Succeed())
		rate, channels, _ := readShape(preserved)
		Expect(rate).To(Equal(uint32(48000)))
		Expect(channels).To(Equal(uint16(2)))

		// The contrast, so this spec fails if the two functions ever converge.
		// AudioToWav shells out to ffmpeg here, which the unit suite cannot
		// assume is installed, so the fold is only asserted when it succeeds.
		if err := AudioToWav(src, folded); err == nil {
			foldedRate, foldedChannels, _ := readShape(folded)
			Expect(foldedRate).To(Equal(uint32(16000)), "AudioToWav still folds")
			Expect(foldedChannels).To(Equal(uint16(1)), "AudioToWav still downmixes")
		}
	})

	It("still passes a 16 kHz mono WAV through", func() {
		src := filepath.Join(dir, "speech.wav")
		dst := filepath.Join(dir, "speech-out.wav")
		writeWAV(src, 16000, 1, 1600)

		Expect(AudioToWavPreservingShape(src, dst)).To(Succeed())

		rate, channels, _ := readShape(dst)
		Expect(rate).To(Equal(uint32(16000)))
		Expect(channels).To(Equal(uint16(1)))
	})

	// The passthrough is about the SHAPE, never about the encoding. An
	// extensible WAV carries plain 16-bit samples behind a 0xFFFE tag, and
	// audio.cpp's reader accepts 16-bit only when the tag is 1, so passing one
	// through unchanged would turn a transcode into "unsupported WAV encoding".
	It("transcodes a WAVE_FORMAT_EXTENSIBLE upload instead of passing it through", func() {
		src := filepath.Join(dir, "extensible.wav")
		dst := filepath.Join(dir, "extensible-out.wav")
		writeExtensibleWAV(src, 44100, 2, 4410)
		Expect(readFormatTag(src)).To(Equal(uint16(0xFFFE)), "the fixture really is extensible")

		Expect(AudioToWavPreservingShape(src, dst)).To(Succeed())

		Expect(readFormatTag(dst)).To(Equal(uint16(1)),
			"the output must be plain PCM, which is all a backend's WAV reader accepts")
		rate, channels, depth := readShape(dst)
		Expect(rate).To(Equal(uint32(44100)), "and the transcode still keeps the rate")
		Expect(channels).To(Equal(uint16(2)), "and the channels")
		Expect(depth).To(Equal(uint16(16)))

		before, err := os.ReadFile(src)
		Expect(err).ToNot(HaveOccurred())
		after, err := os.ReadFile(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(after).ToNot(Equal(before), "it was converted, not hardlinked")
	})

	It("leaves the source file in place", func() {
		src := filepath.Join(dir, "keep.wav")
		dst := filepath.Join(dir, "keep-out.wav")
		writeWAV(src, 44100, 2, 441)

		Expect(AudioToWavPreservingShape(src, dst)).To(Succeed())
		Expect(src).To(BeAnExistingFile(), "the upload must survive the conversion")
	})
})

// AudioResample interpolates the caller's sample rate straight into ffmpeg's
// -ar, and a successful exit does not mean a usable file came out. Measured
// against ffmpeg 7 on a one second 16 kHz clip: -ar 1 exits 0 and writes 78
// bytes, a bare header with no audio behind it, whose declared data size still
// claims 70 bytes so go-audio parses it as a 35 SECOND file. Both the exit
// status and the parsed duration say "audio"; only the bytes on disk say
// otherwise. Before this guard that file was returned to the caller as the
// transformed audio, with a 200.
var _ = Describe("utils/ffmpeg resample output", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			Skip("ffmpeg is not installed")
		}
	})

	It("refuses an output ffmpeg wrote with no audio in it", func() {
		src := filepath.Join(dir, "speech.wav")
		writeWAV(src, 16000, 1, 16000)

		dst, err := AudioResample(src, 1)

		Expect(err).To(HaveOccurred(),
			"a header with no audio must not be handed back as the transformed audio")
		Expect(err.Error()).To(ContainSubstring("no audio"))
		Expect(dst).To(BeEmpty())
	})

	It("resamples normally at a rate inside the endpoint's bounds", func() {
		src := filepath.Join(dir, "speech.wav")
		writeWAV(src, 16000, 1, 16000)

		dst, err := AudioResample(src, 8000)

		Expect(err).ToNot(HaveOccurred())
		rate, channels, _ := readShape(dst)
		Expect(rate).To(Equal(uint32(8000)))
		Expect(channels).To(Equal(uint16(1)))
		info, err := os.Stat(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Size()).To(BeNumerically(">", 8000), "half a second of 8 kHz mono is 8 kB of PCM")
	})

	It("is a no-op for an unset rate", func() {
		src := filepath.Join(dir, "speech.wav")
		writeWAV(src, 16000, 1, 160)

		dst, err := AudioResample(src, 0)

		Expect(err).ToNot(HaveOccurred())
		Expect(dst).To(Equal(src))
	})
})
