package utils_test

import (
	"encoding/binary"
	"os"
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

	It("leaves the source file in place", func() {
		src := filepath.Join(dir, "keep.wav")
		dst := filepath.Join(dir, "keep-out.wav")
		writeWAV(src, 44100, 2, 441)

		Expect(AudioToWavPreservingShape(src, dst)).To(Succeed())
		Expect(src).To(BeAnExistingFile(), "the upload must survive the conversion")
	})
})
