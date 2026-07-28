package localai_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func voiceProfileWAV(duration time.Duration) []byte {
	const sampleRate = 16000
	samples := int(duration.Seconds() * sampleRate)
	dataSize := samples * 2
	buf := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

func voiceProfileMultipart(consent string) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	Expect(writer.WriteField("name", "Documentary narrator")).To(Succeed())
	Expect(writer.WriteField("description", "Measured and clear")).To(Succeed())
	Expect(writer.WriteField("language", "en-US")).To(Succeed())
	Expect(writer.WriteField("transcript", "The exact words spoken in this reference.")).To(Succeed())
	Expect(writer.WriteField("consent_confirmed", consent)).To(Succeed())
	part, err := writer.CreateFormFile("audio", "reference.wav")
	Expect(err).NotTo(HaveOccurred())
	_, err = part.Write(voiceProfileWAV(2 * time.Second))
	Expect(err).NotTo(HaveOccurred())
	Expect(writer.Close()).To(Succeed())
	return body, writer.FormDataContentType()
}

var _ = Describe("Voice profile endpoints", func() {
	var (
		e     *echo.Echo
		store *voiceprofile.Store
	)

	BeforeEach(func() {
		store = voiceprofile.NewStore(GinkgoT().TempDir())
		DeferCleanup(func() { Expect(store.Close()).To(Succeed()) })
		e = echo.New()
		e.GET("/api/voice-profiles", ListVoiceProfilesEndpoint(store))
		e.POST("/api/voice-profiles", CreateVoiceProfileEndpoint(store))
		e.GET("/api/voice-profiles/:id/audio", ServeVoiceProfileAudioEndpoint(store))
		e.DELETE("/api/voice-profiles/:id", DeleteVoiceProfileEndpoint(store))
	})

	It("creates, lists, previews with ranges, and deletes a multipart profile", func() {
		body, contentType := voiceProfileMultipart("true")
		request := httptest.NewRequest(http.MethodPost, "/api/voice-profiles", body)
		request.Header.Set(echo.HeaderContentType, contentType)
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusCreated), recorder.Body.String())

		var created voiceprofile.Profile
		Expect(json.Unmarshal(recorder.Body.Bytes(), &created)).To(Succeed())
		Expect(created.Name).To(Equal("Documentary narrator"))
		Expect(created.Voice).To(Equal(voiceprofile.Reference(created.ID)))

		request = httptest.NewRequest(http.MethodGet, "/api/voice-profiles", nil)
		recorder = httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		var listed VoiceProfileListResponse
		Expect(json.Unmarshal(recorder.Body.Bytes(), &listed)).To(Succeed())
		Expect(listed.Data).To(ConsistOf(created))

		request = httptest.NewRequest(http.MethodGet, "/api/voice-profiles/"+created.ID+"/audio", nil)
		request.Header.Set("Range", "bytes=0-3")
		recorder = httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusPartialContent))
		Expect(recorder.Body.String()).To(Equal("RIFF"))
		Expect(recorder.Header().Get(echo.HeaderCacheControl)).To(Equal("private, no-store"))

		request = httptest.NewRequest(http.MethodDelete, "/api/voice-profiles/"+created.ID, nil)
		recorder = httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusNoContent))
	})

	It("accepts the JSON base64 shape used by the admin MCP client", func() {
		payload, err := json.Marshal(CreateVoiceProfileRequest{
			Name:             "MCP voice",
			Transcript:       "An exact reference transcript.",
			AudioBase64:      base64.StdEncoding.EncodeToString(voiceProfileWAV(time.Second)),
			ConsentConfirmed: true,
		})
		Expect(err).NotTo(HaveOccurred())
		request := httptest.NewRequest(http.MethodPost, "/api/voice-profiles", bytes.NewReader(payload))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusCreated), recorder.Body.String())
	})

	It("rejects creation without explicit consent", func() {
		body, contentType := voiceProfileMultipart("false")
		request := httptest.NewRequest(http.MethodPost, "/api/voice-profiles", body)
		request.Header.Set(echo.HeaderContentType, contentType)
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		var response schema.ErrorResponse
		Expect(json.Unmarshal(recorder.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Error.Message).To(ContainSubstring("consent"))
	})
})
