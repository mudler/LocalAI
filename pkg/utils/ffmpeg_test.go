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

// generateTestWav creates a WAV file with a sine-ish tone at the given sample rate,
// channels, and bit depth (only 16-bit supported for simplicity).
func generateTestWav(path string, sampleRate uint32, numChannels uint16, numSamples int) {
	GinkgoHelper()
	f, err := os.Create(path)
	Expect(err).NotTo(HaveOccurred())
	defer f.Close()

	bitsPerSample := uint16(16)
	blockAlign := numChannels * (bitsPerSample / 8)
	byteRate := sampleRate * uint32(blockAlign)
	totalSamples := numSamples * int(numChannels)
	dataSize := uint32(totalSamples) * uint32(bitsPerSample/8)

	hdr := laudio.WAVHeader{
		ChunkID:       [4]byte{'R', 'I', 'F', 'F'},
		ChunkSize:     36 + dataSize,
		Format:        [4]byte{'W', 'A', 'V', 'E'},
		Subchunk1ID:   [4]byte{'f', 'm', 't', ' '},
		Subchunk1Size: 16,
		AudioFormat:   1,
		NumChannels:   numChannels,
		SampleRate:    sampleRate,
		ByteRate:      byteRate,
		BlockAlign:    blockAlign,
		BitsPerSample: bitsPerSample,
		Subchunk2ID:   [4]byte{'d', 'a', 't', 'a'},
		Subchunk2Size: dataSize,
	}
	Expect(binary.Write(f, binary.LittleEndian, &hdr)).To(Succeed())

	for i := 0; i < totalSamples; i++ {
		sample := int16(1000 * (i % 100))
		Expect(binary.Write(f, binary.LittleEndian, sample)).To(Succeed())
	}
}

var _ = Describe("utils/ffmpeg AudioToWav", func() {
	var src, dst string

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		src = filepath.Join(dir, "input.wav")
		dst = filepath.Join(dir, "output.wav")
	})

	It("keeps a WAV that already has the right format", func() {
		generateTestWav(src, 16000, 1, 1600)

		Expect(AudioToWav(src, dst)).To(Succeed())

		info, err := os.Stat(dst)
		Expect(err).NotTo(HaveOccurred(), "output not found")
		Expect(info.Size()).NotTo(BeZero(), "output file is empty")
	})

	It("resamples a 22050 Hz WAV to 16000 Hz", func() {
		generateTestWav(src, 22050, 1, 22050)

		Expect(AudioToWav(src, dst)).To(Succeed())

		info, err := os.Stat(dst)
		Expect(err).NotTo(HaveOccurred(), "output not found")
		Expect(info.Size()).NotTo(BeZero(), "output file is empty")

		verifyWavFormat(dst, 16000, 1)
	})

	It("downmixes a stereo WAV to mono", func() {
		generateTestWav(src, 16000, 2, 1600)

		Expect(AudioToWav(src, dst)).To(Succeed())

		verifyWavFormat(dst, 16000, 1)
	})

	It("downmixes and resamples a stereo 44100 Hz WAV", func() {
		generateTestWav(src, 44100, 2, 44100)

		Expect(AudioToWav(src, dst)).To(Succeed())

		verifyWavFormat(dst, 16000, 1)
	})
})

func verifyWavFormat(path string, expectedRate uint32, expectedChannels uint16) {
	GinkgoHelper()
	f, err := os.Open(path)
	Expect(err).NotTo(HaveOccurred())
	defer f.Close()

	var hdr laudio.WAVHeader
	Expect(binary.Read(f, binary.LittleEndian, &hdr)).To(Succeed(), "read header")

	Expect(hdr.SampleRate).To(Equal(expectedRate), "sample rate")
	Expect(hdr.NumChannels).To(Equal(expectedChannels), "channels")
	Expect(hdr.BitsPerSample).To(Equal(uint16(16)), "bit depth")
	Expect(hdr.AudioFormat).To(Equal(uint16(1)), "audio format (PCM)")
}
