package openai

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

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
// @accept multipart/form-data
// @Param model formData string true "model"
// @Param file formData file true "file"
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
		responseFormat := c.FormValue("response_format")

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

		tr, err := backend.ModelTranscription(c.Request().Context(), dst, input.Language, input.Translate, diarize, prompt, ml, *config, appConfig)
		if err != nil {
			return err
		}

		xlog.Debug("Transcribed", "transcription", tr)

		switch responseFormat {
		case "json":
			tr.Segments = nil
			return c.JSON(http.StatusOK, tr)
		case "text":
			return c.String(http.StatusOK, processText(tr))
		case "lrc":
			return c.String(http.StatusOK, processLrc(tr))
		case "srt":
			return c.String(http.StatusOK, processSrt(tr))
		case "vtt":
			return c.String(http.StatusOK, processVtt(tr))
		case "json_verbose", "":
			fallthrough
		default:
			return c.JSON(http.StatusOK, tr)
		}
	}
}

func processText(tr *schema.TranscriptionResult) string {
	out := ""
	for _, s := range tr.Segments {
		out += fmt.Sprintf("\n%s", strings.TrimSpace(s.Text))
	}
	return out
}

func processLrc(tr *schema.TranscriptionResult) string {
	out := "[by:LocalAI]\n[re:LocalAI]\n"
	for _, s := range tr.Segments {
		m := s.Start.Milliseconds()
		out += fmt.Sprintf("\n[%02d:%02d:%02d] %s", m/60000, (m/1000)%60, (m%1000)/10, strings.TrimSpace(s.Text))
	}
	return out
}

func processSrt(tr *schema.TranscriptionResult) string {
	out := ""
	for i, s := range tr.Segments {
		out += fmt.Sprintf("\n\n%d\n%s --> %s\n%s", i+1, durationStr(s.Start, ','), durationStr(s.End, ','), strings.TrimSpace(s.Text))
	}
	return out
}

func processVtt(tr *schema.TranscriptionResult) string {
	out := "WEBVTT"
	for _, s := range tr.Segments {
		out += fmt.Sprintf("\n\n%s --> %s\n%s\n", durationStr(s.Start, '.'), durationStr(s.End, '.'), strings.TrimSpace(s.Text))
	}
	return out
}

func durationStr(d time.Duration, millisSeparator rune) string {
	m := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", m/3600000, m/60000, int(d.Seconds())%60, millisSeparator, m%1000)
}
