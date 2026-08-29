package openai

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("decodeRealtimeCallRequest", func() {
	It("decodes the legacy JSON request", func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", bytes.NewBufferString(`{"sdp":"offer","model":"voice","localai_assistant":true}`))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)

		req, plainSDPResponse, err := decodeRealtimeCallRequest(echo.New().NewContext(request, httptest.NewRecorder()))

		Expect(err).NotTo(HaveOccurred())
		Expect(req).To(Equal(RealtimeCallRequest{SDP: "offer", Model: "voice", LocalAIAssistant: true}))
		Expect(plainSDPResponse).To(BeFalse())
	})

	It("decodes the OpenAI multipart request", func() {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		sdpHeader := make(textproto.MIMEHeader)
		sdpHeader.Set("Content-Disposition", `form-data; name="sdp"`)
		sdpHeader.Set("Content-Type", "application/sdp")
		sdpPart, err := writer.CreatePart(sdpHeader)
		Expect(err).NotTo(HaveOccurred())
		_, err = sdpPart.Write([]byte("offer"))
		Expect(err).NotTo(HaveOccurred())
		sessionHeader := make(textproto.MIMEHeader)
		sessionHeader.Set("Content-Disposition", `form-data; name="session"`)
		sessionHeader.Set("Content-Type", echo.MIMEApplicationJSON)
		sessionPart, err := writer.CreatePart(sessionHeader)
		Expect(err).NotTo(HaveOccurred())
		_, err = sessionPart.Write([]byte(`{"type":"realtime","model":"voice","localai_assistant":true}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(writer.Close()).To(Succeed())

		request := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", &body)
		request.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
		req, plainSDPResponse, err := decodeRealtimeCallRequest(echo.New().NewContext(request, httptest.NewRecorder()))

		Expect(err).NotTo(HaveOccurred())
		Expect(req).To(Equal(RealtimeCallRequest{SDP: "offer", Model: "voice", LocalAIAssistant: true}))
		Expect(plainSDPResponse).To(BeTrue())
	})

	It("decodes a raw SDP request with the model query parameter", func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?model=voice", bytes.NewBufferString("offer"))
		request.Header.Set(echo.HeaderContentType, "application/sdp")

		req, plainSDPResponse, err := decodeRealtimeCallRequest(echo.New().NewContext(request, httptest.NewRecorder()))

		Expect(err).NotTo(HaveOccurred())
		Expect(req).To(Equal(RealtimeCallRequest{SDP: "offer", Model: "voice"}))
		Expect(plainSDPResponse).To(BeTrue())
	})
})

var _ = Describe("writeRealtimeCallResponse", func() {
	It("writes the bare SDP answer for OpenAI request formats", func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", nil)
		context := echo.New().NewContext(request, response)

		Expect(writeRealtimeCallResponse(context, true, "answer", "session-id")).To(Succeed())

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(response.Header().Get(echo.HeaderContentType)).To(Equal("application/sdp"))
		Expect(response.Body.String()).To(Equal("answer"))
	})

	It("preserves the JSON response for legacy requests", func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", nil)
		context := echo.New().NewContext(request, response)

		Expect(writeRealtimeCallResponse(context, false, "answer", "session-id")).To(Succeed())

		Expect(response.Code).To(Equal(http.StatusCreated))
		Expect(response.Header().Get(echo.HeaderContentType)).To(Equal(echo.MIMEApplicationJSON))
		Expect(response.Body.String()).To(MatchJSON(`{"sdp":"answer","session_id":"session-id"}`))
	})
})
