package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"
)

// TranscriptEndpoint is the OpenAI Whisper API endpoint https://platform.openai.com/docs/api-reference/audio/create
// @Summary Transcribes audio into the input language.
// @Tags audio
// @accept multipart/form-data
// @Param model formData string true "model"
// @Param file formData file true "file"
// @Param temperature formData number false "sampling temperature"
// @Param timestamp_granularities formData []string false "timestamp granularities (word, segment)"
// @Param stream formData boolean false "stream partial results as SSE"
// @Success 200 {object} map[string]string	 "Response"
// @Router /v1/audio/transcriptions [post]
func TranscriptEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return echo.ErrBadRequest
		}

		diarize := c.FormValue("diarize") != "false"
		prompt := c.FormValue("prompt")
		responseFormat := schema.TranscriptionResponseFormatType(c.FormValue("response_format"))

		// OpenAI accepts `temperature` as a string in multipart form. Tolerate
		// missing/invalid values rather than failing the whole request.
		var temperature float32
		if v := c.FormValue("temperature"); v != "" {
			if t, err := strconv.ParseFloat(v, 32); err == nil {
				temperature = float32(t)
			}
		}

		// timestamp_granularities[] is a multi-value form field per the OpenAI spec.
		// Echo exposes all values for a key via FormParams.
		var timestampGranularities []string
		if form, err := c.FormParams(); err == nil {
			for _, key := range []string{"timestamp_granularities[]", "timestamp_granularities"} {
				if vals, ok := form[key]; ok {
					for _, v := range vals {
						v = strings.TrimSpace(v)
						if v != "" {
							timestampGranularities = append(timestampGranularities, v)
						}
					}
				}
			}
		}

		stream := false
		if v := c.FormValue("stream"); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				stream = b
			}
		}

		// retrieve the file data from the request
		file, err := c.FormFile("file")
		if err != nil {
			return err
		}
		f, err := file.Open()
		if err != nil {
			return err
		}
		defer f.Close()

		dir, err := os.MkdirTemp("", "whisper")

		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)

		dst := filepath.Join(dir, path.Base(file.Filename))
		dstFile, err := os.Create(dst)
		if err != nil {
			return err
		}

		if _, err := io.Copy(dstFile, f); err != nil {
			xlog.Debug("Audio file copying error", "filename", file.Filename, "dst", dst, "error", err)
			return err
		}

		xlog.Debug("Audio file copied", "dst", dst)

		req := backend.TranscriptionRequest{
			Audio:                  dst,
			Language:               input.Language,
			Translate:              input.Translate,
			Diarize:                diarize,
			Prompt:                 prompt,
			Temperature:            temperature,
			TimestampGranularities: timestampGranularities,
		}

		if stream {
			return streamTranscription(c, req, ml, *config, appConfig)
		}

		tr, err := backend.ModelTranscriptionWithOptions(req, ml, *config, appConfig)
		if err != nil {
			return err
		}

		xlog.Debug("Transcribed", "transcription", tr)

		switch responseFormat {
		case schema.TranscriptionResponseFormatLrc, schema.TranscriptionResponseFormatText, schema.TranscriptionResponseFormatSrt, schema.TranscriptionResponseFormatVtt:
			return c.String(http.StatusOK, schema.TranscriptionResponse(tr, responseFormat))
		case schema.TranscriptionResponseFormatJson:
			tr.Segments = nil
			fallthrough
		case schema.TranscriptionResponseFormatJsonVerbose, "": // maintain backwards compatibility
			return c.JSON(http.StatusOK, tr)
		default:
			return errors.New("invalid response_format")
		}
	}
}

// streamTranscription emits OpenAI-format SSE events for a transcription
// request: one `transcript.text.delta` per backend chunk, a final
// `transcript.text.done` with the assembled text, and `[DONE]`. Backends that
// can't truly stream still produce a single Final event, which we surface as
// one delta + done.
func streamTranscription(c echo.Context, req backend.TranscriptionRequest, ml *model.ModelLoader, config config.ModelConfig, appConfig *config.ApplicationConfig) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	writeEvent := func(payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Response().Writer, "data: %s\n\n", data); err != nil {
			return err
		}
		c.Response().Flush()
		return nil
	}

	var assembled strings.Builder
	var finalResult *schema.TranscriptionResult

	err := backend.ModelTranscriptionStream(req, ml, config, appConfig, func(chunk backend.TranscriptionStreamChunk) {
		if chunk.Delta != "" {
			assembled.WriteString(chunk.Delta)
			_ = writeEvent(map[string]any{
				"type":  "transcript.text.delta",
				"delta": chunk.Delta,
			})
		}
		if chunk.Final != nil {
			finalResult = chunk.Final
		}
	})
	if err != nil {
		errPayload := map[string]any{
			"type": "error",
			"error": map[string]any{
				"message": err.Error(),
				"type":    "server_error",
			},
		}
		_ = writeEvent(errPayload)
		_, _ = fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
		c.Response().Flush()
		return nil
	}

	// Build the final event. Prefer the backend-provided final result; if the
	// backend only emitted deltas, synthesize the result from what we collected.
	if finalResult == nil {
		finalResult = &schema.TranscriptionResult{Text: assembled.String()}
	} else if finalResult.Text == "" && assembled.Len() > 0 {
		finalResult.Text = assembled.String()
	}
	// If the backend never produced a delta but did return a final text, emit
	// it as a single delta so clients always see at least one delta event.
	if assembled.Len() == 0 && finalResult.Text != "" {
		_ = writeEvent(map[string]any{
			"type":  "transcript.text.delta",
			"delta": finalResult.Text,
		})
	}
	_ = writeEvent(map[string]any{
		"type": "transcript.text.done",
		"text": finalResult.Text,
	})
	_, _ = fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
	c.Response().Flush()
	return nil
}
