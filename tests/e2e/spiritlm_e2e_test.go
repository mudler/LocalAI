package e2e_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/v3"
)

var _ = Describe("SpiritLM backend E2E", Label("SpiritLM"), func() {
	Describe("Chat completions", func() {
		It("returns response for spirit-lm-base-7b", func() {
			resp, err := client.Chat.Completions.New(
				context.TODO(),
				openai.ChatCompletionNewParams{
					Model: "spirit-lm-base-7b",
					Messages: []openai.ChatCompletionMessageParamUnion{
						openai.UserMessage("Say hello."),
					},
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(resp.Choices)).To(Equal(1))
			Expect(resp.Choices[0].Message.Content).ToNot(BeEmpty())
		})
	})

	Describe("TTS", func() {
		It("returns audio for spirit-lm-base-7b", func() {
			body := `{"model":"spirit-lm-base-7b","input":"Hello","voice":"default"}`
			req, err := http.NewRequest("POST", apiURL+"/audio/speech", io.NopCloser(strings.NewReader(body)))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("audio/"))
			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(data)).To(BeNumerically(">", 0))
		})
	})

	Describe("Transcription", func() {
		It("returns transcription for spirit-lm-base-7b", func() {
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			part, err := w.CreateFormFile("file", "audio.wav")
			Expect(err).ToNot(HaveOccurred())
			_, _ = part.Write(minimalWAVBytes())
			_ = w.WriteField("model", "spirit-lm-base-7b")
			Expect(w.Close()).To(Succeed())

			req, err := http.NewRequest("POST", apiURL+"/audio/transcriptions", &buf)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", w.FormDataContentType())

			httpClient := &http.Client{Timeout: 30 * time.Second}
			resp, err := httpClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))
			data, err := io.ReadAll(resp.Body)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("mocked"))
		})
	})
})

func minimalWAVBytes() []byte {
	const sampleRate = 16000
	const numChannels = 1
	const bitsPerSample = 16
	const numSamples = 160
	dataSize := numSamples * numChannels * (bitsPerSample / 8)
	headerLen := 44
	var buf bytes.Buffer
	buf.Write([]byte("RIFF"))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(headerLen-8+dataSize))
	buf.Write([]byte("WAVEfmt "))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*numChannels*(bitsPerSample/8)))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(numChannels*(bitsPerSample/8)))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.Write([]byte("data"))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}
