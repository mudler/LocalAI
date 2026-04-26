package localai

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/utils"
)

// Match `data:<mime>[;param=value...];base64,` — MediaRecorder in the browser
// produces data URIs like `data:audio/webm;codecs=opus;base64,...`, so the
// pre-`;base64,` section can contain zero or more parameter segments. The
// old `([^;]+)` form only matched exactly one segment and left recordings
// from the React UI's live-capture tab unparsed, which then failed base64
// decoding on the leading `data:` bytes.
var audioDataURIPattern = regexp.MustCompile(`^data:[^,]+?;base64,`)

var audioDownloadClient = http.Client{Timeout: 30 * time.Second}

// decodeAudioInput materialises a URL / data-URI / raw-base64 audio
// payload to a temporary file and returns its path plus a cleanup
// function. Voice backends expect a filesystem path (same convention
// as TranscriptRequest.dst) — callers must defer the returned cleanup
// so the temp file does not leak.
//
// Bad inputs (invalid URL, undecodable base64, non-audio payload) are
// surfaced as 400 Bad Request rather than 500 so API consumers can
// distinguish a client mistake from a server failure.
func decodeAudioInput(s string) (string, func(), error) {
	if s == "" {
		return "", func() {}, echo.NewHTTPError(http.StatusBadRequest, "audio input is empty")
	}

	var raw []byte
	switch {
	case strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"):
		if err := utils.ValidateExternalURL(s); err != nil {
			return "", func() {}, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid audio URL: %v", err))
		}
		resp, err := audioDownloadClient.Get(s)
		if err != nil {
			return "", func() {}, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("audio download failed: %v", err))
		}
		defer resp.Body.Close()
		raw, err = io.ReadAll(resp.Body)
		if err != nil {
			return "", func() {}, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("audio download read failed: %v", err))
		}
	default:
		payload := s
		if m := audioDataURIPattern.FindString(s); m != "" {
			payload = strings.Replace(s, m, "", 1)
		}
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", func() {}, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid audio base64: %v", err))
		}
		raw = decoded
	}

	if len(raw) == 0 {
		return "", func() {}, echo.NewHTTPError(http.StatusBadRequest, "audio input decoded to zero bytes")
	}

	f, err := os.CreateTemp("", "localai-voice-*.wav")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := f.Write(raw); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}
