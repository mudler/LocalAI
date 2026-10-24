package openai

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wavSampleRateOf reads the sample rate from a WAV file's fmt chunk
// (RIFF/WAVE, little-endian), without external dependencies.
func wavSampleRateOf(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(b) < 44 || !bytes.Equal(b[0:4], []byte("RIFF")) ||
		!bytes.Equal(b[8:12], []byte("WAVE")) ||
		!bytes.Equal(b[12:16], []byte("fmt ")) {
		return 0, fmt.Errorf("not a RIFF/WAVE file: %s", path)
	}
	return int(binary.LittleEndian.Uint32(b[24:28])), nil
}

// Regression test: the committed-utterance WAV must carry the session's
// input sample rate in its header. The client-commit path used to write a
// FIXED 16 kHz header (laudio.NewWAVHeader) while the input buffer holds
// audio at the session input rate (e.g. 24 kHz for OpenAI-Realtime clients).
// The file-based transcription backend then decoded 24 kHz data at 16 kHz
// (1.5x speed/pitch), degrading transcription to garbage or empty output,
// which failed the follow-up response with input_not_committed (total
// silence after the greeting).
var _ = Describe("writeUtteranceWAV", func() {
	It("writes the header at the requested sample rate (24 kHz input)", func() {
		utt := make([]byte, 24000*2) // 1 s of 16-bit mono PCM
		path, cleanup, err := writeUtteranceWAV(utt, 24000)
		defer cleanup()
		Expect(err).ToNot(HaveOccurred())
		rate, herr := wavSampleRateOf(path)
		Expect(herr).ToNot(HaveOccurred())
		Expect(rate).To(Equal(24000))
	})

	It("writes the header at the requested sample rate (16 kHz input)", func() {
		utt := make([]byte, 16000*2)
		path, cleanup, err := writeUtteranceWAV(utt, 16000)
		defer cleanup()
		Expect(err).ToNot(HaveOccurred())
		rate, herr := wavSampleRateOf(path)
		Expect(herr).ToNot(HaveOccurred())
		Expect(rate).To(Equal(16000))
	})
})
