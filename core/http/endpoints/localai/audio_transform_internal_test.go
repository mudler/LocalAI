package localai

import (
	"bytes"
	"encoding/binary"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	laudio "github.com/mudler/LocalAI/pkg/audio"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pcm16WAV renders a 16-bit PCM WAV carrying a ramp seeded from `seed`, so two
// fixtures of the same shape still differ byte for byte.
func pcm16WAV(sampleRate uint32, channels uint16, frames int, seed int) []byte {
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
	buf := &bytes.Buffer{}
	Expect(binary.Write(buf, binary.LittleEndian, &hdr)).To(Succeed())
	for i := 0; i < total; i++ {
		Expect(binary.Write(buf, binary.LittleEndian, int16(seed+7*(i%100)))).To(Succeed())
	}
	return buf.Bytes()
}

// multipartFiles builds a real multipart form and parses it back, which is the
// only honest way to get *multipart.FileHeader values shaped exactly like the
// ones echo hands the endpoint. `parts` maps the form field name to the client
// side FILE NAME, which is the part of the request this fixture is about.
func multipartFiles(parts map[string]string, bodies map[string][]byte) map[string]*multipart.FileHeader {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for field, filename := range parts {
		part, err := writer.CreateFormFile(field, filename)
		Expect(err).ToNot(HaveOccurred())
		_, err = part.Write(bodies[field])
		Expect(err).ToNot(HaveOccurred())
	}
	Expect(writer.Close()).To(Succeed())

	reader := multipart.NewReader(bytes.NewReader(body.Bytes()), writer.Boundary())
	form, err := reader.ReadForm(1 << 20)
	Expect(err).ToNot(HaveOccurred())

	headers := map[string]*multipart.FileHeader{}
	for field := range parts {
		Expect(form.File[field]).To(HaveLen(1))
		headers[field] = form.File[field][0]
	}
	return headers
}

// `sample_rate` is interpolated straight into ffmpeg's -ar by
// utils.AudioResample. Before this bound existed the field was accepted
// unchanged, and -ar 999999999 on a one second clip writes a 3.9 GB WAV and
// exits 0, into a GeneratedContentDir nothing sweeps; a separation repeats that
// per stem. At the other end -ar 1 writes a header with no audio in it, also
// exit 0, which was then served as the response body.
var _ = Describe("audio transform sample_rate bounds", func() {
	status := func(err error) int {
		Expect(err).To(HaveOccurred())
		httpErr, ok := err.(*echo.HTTPError)
		Expect(ok).To(BeTrue(), "the rejection has to be an HTTP status, not a 500")
		return httpErr.Code
	}

	It("accepts an unset sample_rate, which means the backend's own rate", func() {
		Expect(validateAudioTransformSampleRate(0)).To(Succeed())
	})

	It("accepts both ends of the supported range", func() {
		Expect(validateAudioTransformSampleRate(minAudioTransformSampleRate)).To(Succeed())
		Expect(validateAudioTransformSampleRate(maxAudioTransformSampleRate)).To(Succeed())
		Expect(validateAudioTransformSampleRate(48000)).To(Succeed())
	})

	It("rejects a rate below the lower bound with a 400", func() {
		Expect(status(validateAudioTransformSampleRate(minAudioTransformSampleRate - 1))).
			To(Equal(http.StatusBadRequest))
		Expect(status(validateAudioTransformSampleRate(1))).To(Equal(http.StatusBadRequest))
		Expect(status(validateAudioTransformSampleRate(-1))).To(Equal(http.StatusBadRequest))
	})

	It("rejects a rate above the upper bound with a 400", func() {
		Expect(status(validateAudioTransformSampleRate(maxAudioTransformSampleRate + 1))).
			To(Equal(http.StatusBadRequest))
		Expect(status(validateAudioTransformSampleRate(999999999))).To(Equal(http.StatusBadRequest))
	})

	It("names both bounds and the offending value in the message", func() {
		err := validateAudioTransformSampleRate(999999999)
		Expect(err).To(HaveOccurred())
		Expect(err.(*echo.HTTPError).Message).To(ContainSubstring("8000"))
		Expect(err.(*echo.HTTPError).Message).To(ContainSubstring("192000"))
		Expect(err.(*echo.HTTPError).Message).To(ContainSubstring("999999999"))
	})
})

var _ = Describe("audio transform stream model validation", func() {
	It("accepts models that advertise audio transforms", func() {
		usecases := config.FLAG_AUDIO_TRANSFORM
		cfg := &config.ModelConfig{KnownUsecases: &usecases}
		Expect(validateAudioTransformStreamModel(cfg)).To(Succeed())
	})

	It("rejects realtime any-to-any models", func() {
		usecases := config.FLAG_REALTIME_AUDIO
		cfg := &config.ModelConfig{KnownUsecases: &usecases, Backend: "liquid-audio"}
		err := validateAudioTransformStreamModel(cfg)
		Expect(err).To(MatchError(ContainSubstring("OpenAI Realtime API")))
	})
})

// Both uploads land in the SAME temp dir, and a client is free to send the same
// BASENAME on both parts (`-F audio=@mic/clip.wav -F reference=@loopback/clip.wav`
// is an ordinary request, not an attack). The raw copy is therefore named after
// the FORM FIELD as well as the file: without that both parts wrote
// "raw-clip.wav", and because AudioToWavPreservingShape hardlinks a WAV that is
// already PCM16 rather than copying it, the reference part's os.Create
// truncated the very inode audio.wav pointed at. The request still returned
// 200, with the primary input replaced by the reference.
var _ = Describe("saveMultipartFileAsWAV", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	It("keeps the two parts apart when they share a filename", func() {
		audioBody := pcm16WAV(16000, 1, 1600, 1000)
		referenceBody := pcm16WAV(16000, 1, 1600, -1000)
		Expect(audioBody).ToNot(Equal(referenceBody), "the fixtures must be distinguishable")

		headers := multipartFiles(
			map[string]string{"audio": "clip.wav", "reference": "clip.wav"},
			map[string][]byte{"audio": audioBody, "reference": referenceBody},
		)

		audioPath, err := saveMultipartFileAsWAV(headers["audio"], dir, "audio", false)
		Expect(err).ToNot(HaveOccurred())
		referencePath, err := saveMultipartFileAsWAV(headers["reference"], dir, "reference", false)
		Expect(err).ToNot(HaveOccurred())

		onDiskAudio, err := os.ReadFile(audioPath)
		Expect(err).ToNot(HaveOccurred())
		onDiskReference, err := os.ReadFile(referencePath)
		Expect(err).ToNot(HaveOccurred())

		Expect(onDiskAudio).To(Equal(audioBody),
			"the primary input must still be the audio part after the reference is written")
		Expect(onDiskReference).To(Equal(referenceBody))
		Expect(onDiskAudio).ToNot(Equal(onDiskReference),
			"identical mic and reference makes an echo canceller null everything and return silence with a 200")
	})

	It("gives the two parts distinct raw copies", func() {
		headers := multipartFiles(
			map[string]string{"audio": "clip.wav", "reference": "clip.wav"},
			map[string][]byte{
				"audio":     pcm16WAV(16000, 1, 800, 500),
				"reference": pcm16WAV(16000, 1, 800, -500),
			},
		)

		_, err := saveMultipartFileAsWAV(headers["audio"], dir, "audio", false)
		Expect(err).ToNot(HaveOccurred())
		_, err = saveMultipartFileAsWAV(headers["reference"], dir, "reference", false)
		Expect(err).ToNot(HaveOccurred())

		Expect(filepath.Join(dir, "audio-raw-clip.wav")).To(BeAnExistingFile())
		Expect(filepath.Join(dir, "reference-raw-clip.wav")).To(BeAnExistingFile())
	})

	It("still works when the two parts have different filenames", func() {
		headers := multipartFiles(
			map[string]string{"audio": "mic.wav", "reference": "loopback.wav"},
			map[string][]byte{
				"audio":     pcm16WAV(16000, 1, 400, 300),
				"reference": pcm16WAV(16000, 1, 400, -300),
			},
		)

		audioPath, err := saveMultipartFileAsWAV(headers["audio"], dir, "audio", false)
		Expect(err).ToNot(HaveOccurred())
		referencePath, err := saveMultipartFileAsWAV(headers["reference"], dir, "reference", false)
		Expect(err).ToNot(HaveOccurred())
		Expect(audioPath).ToNot(Equal(referencePath))
	})
})
