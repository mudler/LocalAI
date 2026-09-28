package openai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A broken or missing upload is the caller's fault: the audio endpoints must
// answer 400, not surface the multipart parser error as a 500.
var _ = Describe("audio upload endpoints reject bad uploads as client errors", func() {
	type endpointCase struct {
		path    string
		handler func() echo.HandlerFunc
	}

	cases := map[string]endpointCase{
		"transcription": {"/v1/audio/transcriptions", func() echo.HandlerFunc {
			return TranscriptEndpoint(nil, nil, config.NewApplicationConfig())
		}},
		"diarization": {"/v1/audio/diarization", func() echo.HandlerFunc {
			return DiarizationEndpoint(nil, nil, config.NewApplicationConfig())
		}},
		"sound classification": {"/v1/audio/classifications", func() echo.HandlerFunc {
			return SoundClassificationEndpoint(nil, nil, config.NewApplicationConfig())
		}},
	}

	run := func(ec endpointCase, contentType, body string) error {
		req := httptest.NewRequest(http.MethodPost, ec.path, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.OpenAIRequest{PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "m"}}})
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{})
		return ec.handler()(c)
	}

	expectBadRequest := func(err error) {
		Expect(err).To(HaveOccurred())
		var he *echo.HTTPError
		Expect(errors.As(err, &he)).To(BeTrue(), "expected *echo.HTTPError, got %T: %v", err, err)
		Expect(he.Code).To(Equal(http.StatusBadRequest))
	}

	// An unknown response_format is the caller's fault too, and must be
	// rejected before the backend runs: a failover chain would otherwise
	// transcribe on every target and count the error against each of them.
	for name, ec := range map[string]endpointCase{
		"transcription": cases["transcription"],
		"diarization":   cases["diarization"],
	} {
		It(name+": unknown response_format", func() {
			body := "--xyz\r\nContent-Disposition: form-data; name=\"response_format\"\r\n\r\nbogus\r\n" +
				"--xyz\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n\r\nRIFF\r\n--xyz--\r\n"
			var err error
			Expect(func() { err = run(ec, "multipart/form-data; boundary=xyz", body) }).NotTo(Panic(), "the backend must not be reached")
			expectBadRequest(err)
		})
	}

	for name, ec := range cases {
		It(name+": multipart content type without a boundary", func() {
			expectBadRequest(run(ec, "multipart/form-data", ""))
		})
		It(name+": multipart body without the file field", func() {
			body := "--xyz\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nm\r\n--xyz--\r\n"
			expectBadRequest(run(ec, "multipart/form-data; boundary=xyz", body))
		})
	}
})
