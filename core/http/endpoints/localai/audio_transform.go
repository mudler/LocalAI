package localai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// audioTransformWSUpgrader allows WebSocket connections from any origin —
// matches the realtime endpoint's policy. Authentication is handled at the
// HTTP layer before the upgrade.
var audioTransformWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// lockedConn wraps a WebSocket connection so the backend-forwarding goroutine
// and the read loop can both emit frames without tripping Gorilla's
// single-concurrent-writer rule. Without this the two writers race and can
// panic with "concurrent write to websocket connection" (and a -race build
// flags the data race directly). Reads stay on the single read loop, so only
// writes need serialising. Mirrors the openresponses endpoint's lockedConn.
type lockedConn struct {
	*websocket.Conn
	writeMu sync.Mutex
}

func (lc *lockedConn) WriteMessage(messageType int, data []byte) error {
	lc.writeMu.Lock()
	defer lc.writeMu.Unlock()
	return lc.Conn.WriteMessage(messageType, data)
}

const (
	// audioTransformWSReadLimit is the per-message ceiling on inbound WS
	// frames. With 16 kHz / 256-sample / s16-stereo (1024 B/frame) the
	// default ceiling is generous; raised here to 1 MiB to allow larger
	// frame_samples for backends with longer hops.
	audioTransformWSReadLimit = 1 << 20

	// minAudioTransformSampleRate / maxAudioTransformSampleRate bound the
	// caller-supplied `sample_rate` form field.
	//
	// THIS IS A RESOURCE BOUND, not a taste judgement. The value is
	// interpolated straight into ffmpeg's -ar by utils.AudioResample, and
	// ffmpeg accepts absurd rates happily: -ar 999999999 on a one second clip
	// writes a 3.9 GB WAV and exits 0, into a GeneratedContentDir that nothing
	// sweeps, and a separation request repeats that for every stem
	// (see convertStems). At the other end -ar 1 writes a ZERO byte file, also
	// exit 0, which was then served to the caller as the transformed audio.
	//
	// 8000 is the lowest rate any telephony codec uses and the lowest anything
	// here is trained on; 192000 is the highest rate consumer audio hardware
	// and the WAV container are routinely used at, and is already 4x the
	// 48 kHz every model in the gallery produces. `sample_rate` unset (0) still
	// means "leave the backend's own rate alone" and skips the check.
	minAudioTransformSampleRate = 8000
	maxAudioTransformSampleRate = 192000
)

// validateAudioTransformSampleRate returns an HTTP 400 for a requested output
// rate outside [minAudioTransformSampleRate, maxAudioTransformSampleRate].
// Zero means unset and is always accepted.
//
// Split out from the handler so the bound is testable without a model, a
// backend or a multipart body: the handler rejects before it touches disk.
func validateAudioTransformSampleRate(sampleRate int) error {
	if sampleRate == 0 {
		return nil
	}
	if sampleRate < minAudioTransformSampleRate || sampleRate > maxAudioTransformSampleRate {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf(
			"sample_rate must be between %d and %d Hz (got %d)",
			minAudioTransformSampleRate, maxAudioTransformSampleRate, sampleRate))
	}
	return nil
}

func validateAudioTransformStreamModel(cfg *config.ModelConfig) error {
	if cfg != nil && cfg.HasUsecases(config.FLAG_AUDIO_TRANSFORM) {
		return nil
	}
	if cfg != nil && cfg.HasUsecases(config.FLAG_REALTIME_AUDIO) {
		return fmt.Errorf("model %q is an any-to-any model; use the OpenAI Realtime API instead", cfg.Name)
	}
	modelName := ""
	if cfg != nil {
		modelName = cfg.Name
	}
	return fmt.Errorf("model %q does not support streaming audio transforms", modelName)
}

// AudioTransformEndpoint implements the batch audio-transform API. Accepts a
// multipart/form-data request with `audio` (required) and an optional
// `reference` file. Backend-specific tuning is forwarded via repeated
// `params[<key>]=<value>` form fields. Returns the enhanced audio as an
// attachment, mirroring the /v1/audio/speech response shape.
//
// @Summary Transform audio (echo cancellation, noise suppression, voice conversion, etc.)
// @Description Runs an audio-in / audio-out transform conditioned on an optional auxiliary reference signal. Concrete transforms include AEC + noise suppression + dereverberation (LocalVQE), voice conversion (reference = target speaker), and pitch shifting. The backend determines the operation; pass model-specific tuning via repeated `params[<key>]=<value>` form fields.
// @Tags audio
// @Accept multipart/form-data
// @Produce audio/x-wav
// @Param model formData string true "model"
// @Param audio formData file true "primary input audio file"
// @Param reference formData file false "auxiliary reference audio (loopback for AEC, target voice for conversion, etc.)"
// @Param response_format formData string false "wav | mp3 | ogg | flac"
// @Param sample_rate formData integer false "desired output sample rate in Hz; omit for the backend's own rate, otherwise 8000-192000"
// @Success 200 {string} binary "transformed audio file"
// @Router /audio/transformations [post]
// @Router /audio/transform [post]
func AudioTransformEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.AudioTransformRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("LocalAI Audio Transform Request received", "model", input.Model)

		// Before the temp dir and before the model is touched: a rejected rate
		// must not cost an upload, a decode or an inference.
		if err := validateAudioTransformSampleRate(input.SampleRate); err != nil {
			return err
		}

		audioFile, err := c.FormFile("audio")
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "missing required 'audio' file field")
		}

		dir, err := os.MkdirTemp("", "audio-transform")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()

		// Whether the upload is folded to 16 kHz mono is the BACKEND'S
		// declaration and not this endpoint's default. LocalVQE's echo
		// cancellation needs that shape; source separation cannot survive it.
		// See config.AudioTransformRequiresMono16kInput.
		fold := config.AudioTransformRequiresMono16kInput(cfg.Backend)

		audioPath, err := saveMultipartFileAsWAV(audioFile, dir, "audio", fold)
		if err != nil {
			return err
		}

		var referencePath string
		if refFile, err := c.FormFile("reference"); err == nil {
			// The reference is folded on the same terms as the primary input.
			// For AEC the two have to be the same shape to line up sample for
			// sample; for voice conversion the reference is a speaker clip
			// whose rate the family resolves for itself.
			referencePath, err = saveMultipartFileAsWAV(refFile, dir, "reference", fold)
			if err != nil {
				return err
			}
		}

		params := collectParamsFromForm(c)
		// Form-field params override schema-body params on collision.
		for k, v := range input.Params {
			if _, exists := params[k]; !exists {
				params[k] = v
			}
		}

		out, _, err := backend.ModelAudioTransform(c.Request().Context(), audioPath, referencePath, backend.AudioTransformOptions{
			Params: params,
		}, ml, appConfig, *cfg)
		if err != nil {
			return err
		}
		dst := out.Dst

		if input.SampleRate > 0 {
			dst, err = utils.AudioResample(dst, input.SampleRate)
			if err != nil {
				return err
			}
		}

		dst, err = utils.AudioConvert(dst, input.Format)
		if err != nil {
			return err
		}

		dst, contentType := audio.NormalizeAudioFile(dst)
		if contentType != "" {
			c.Response().Header().Set(echo.HeaderContentType, contentType)
		}
		// Expose the persisted inputs so the React UI can save them in
		// history alongside the output. The /generated-audio/ prefix is
		// the same one ttsApi uses (parsed from Content-Disposition).
		if name := filepath.Base(out.AudioPath); name != "" {
			c.Response().Header().Set(echo.HeaderAccessControlExposeHeaders, exposedAudioTransformHeaders)
			c.Response().Header().Set("X-Audio-Input-Url", "/generated-audio/"+name)
		}
		if out.ReferencePath != "" {
			if name := filepath.Base(out.ReferencePath); name != "" {
				c.Response().Header().Set("X-Audio-Reference-Url", "/generated-audio/"+name)
			}
		}
		// The body can carry one file, and a separation produces several from
		// one run. The rest are named here, as JSON, so a caller can fetch the
		// stems it did not ask for without paying for another separation.
		// Header rather than body for the same reason the input URLs are
		// headers: the body is the audio itself.
		//
		// Converted on the same terms as dst, because dst IS one of these stems
		// and the caller is entitled to expect the set to match. Skipping this
		// left a caller who asked for 48 kHz mp3 with an mp3 body and four
		// 44.1 kHz WAV siblings, one of which was supposed to be the same
		// recording as the body.
		if header := stemsHeader(convertStems(out.Stems, input.SampleRate, input.Format)); header != "" {
			c.Response().Header().Set(echo.HeaderAccessControlExposeHeaders, exposedAudioTransformHeaders)
			c.Response().Header().Set("X-Audio-Stems", header)
		}
		return c.Attachment(dst, filepath.Base(dst))
	}
}

// Wire protocol documented in docs/content/features/audio-transform.md
// and on schema.AudioTransformStreamControl.
//
// @Summary Bidirectional realtime audio transform over WebSocket.
// @Description Streams binary PCM frames in (interleaved stereo: ch0=audio, ch1=reference) and out (mono). The model must support the audio_transform use case. Any-to-any models such as liquid-audio use the OpenAI Realtime API instead. The first message must be a JSON `session.update` envelope describing model + sample format + frame size + backend params. Server emits binary PCM on the same cadence.
// @Tags audio
// @Router /audio/transformations/stream [get]
func AudioTransformStreamEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		rawWS, err := audioTransformWSUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		ws := &lockedConn{Conn: rawWS}
		defer func() { _ = ws.Close() }()
		ws.SetReadLimit(audioTransformWSReadLimit)

		mt, payload, err := ws.ReadMessage()
		if err != nil {
			xlog.Debug("audio_transform stream: client closed before session.update", "error", err)
			return nil
		}
		if mt != websocket.TextMessage {
			sendWSError(ws, "expected JSON session.update as first message")
			return nil
		}
		var ctrl schema.AudioTransformStreamControl
		if err := json.Unmarshal(payload, &ctrl); err != nil {
			sendWSError(ws, "invalid JSON: "+err.Error())
			return nil
		}
		if ctrl.Type != schema.AudioTransformCtrlSessionUpdate {
			sendWSError(ws, "first message must be "+schema.AudioTransformCtrlSessionUpdate)
			return nil
		}
		if ctrl.Model == "" {
			sendWSError(ws, "session.update missing model")
			return nil
		}

		cfg, err := app.ModelConfigLoader().LoadModelConfigFileByNameDefaultOptions(ctrl.Model, app.ApplicationConfig())
		if err != nil || cfg == nil {
			sendWSError(ws, fmt.Sprintf("failed to load model config: %v", err))
			return nil
		}
		if err := validateAudioTransformStreamModel(cfg); err != nil {
			sendWSError(ws, err.Error())
			return nil
		}

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		stream, err := backend.ModelAudioTransformStream(ctx, app.ModelLoader(), app.ApplicationConfig(), *cfg)
		if err != nil {
			sendWSError(ws, fmt.Sprintf("failed to open transform stream: %v", err))
			return nil
		}

		sampleFormat, err := parseSampleFormat(ctrl.SampleFormat)
		if err != nil {
			sendWSError(ws, err.Error())
			return nil
		}
		if err := stream.Send(buildConfigRequest(sampleFormat, &ctrl)); err != nil {
			sendWSError(ws, fmt.Sprintf("backend send config: %v", err))
			return nil
		}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				resp, err := stream.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						sendWSError(ws, fmt.Sprintf("backend recv: %v", err))
					}
					return
				}
				if err := ws.WriteMessage(websocket.BinaryMessage, resp.Pcm); err != nil {
					return
				}
			}
		}()

		// Per-connection scratch for stereo de-interleaving — avoids two
		// allocs per inbound binary frame at the 16 ms cadence.
		var audioBuf, refBuf []byte
	readLoop:
		for {
			mt, payload, err := ws.ReadMessage()
			if err != nil {
				_ = stream.CloseSend()
				break readLoop
			}
			switch mt {
			case websocket.BinaryMessage:
				audio, ref := splitStereoFrameInto(payload, sampleFormat, &audioBuf, &refBuf)
				if err := stream.Send(&proto.AudioTransformFrameRequest{
					Payload: &proto.AudioTransformFrameRequest_Frame{
						Frame: &proto.AudioTransformFrame{
							AudioPcm:     audio,
							ReferencePcm: ref,
						},
					},
				}); err != nil {
					sendWSError(ws, fmt.Sprintf("backend send frame: %v", err))
					_ = stream.CloseSend()
					break readLoop
				}
			case websocket.TextMessage:
				var ctrl schema.AudioTransformStreamControl
				if err := json.Unmarshal(payload, &ctrl); err != nil {
					sendWSError(ws, "invalid mid-stream JSON: "+err.Error())
					continue
				}
				switch ctrl.Type {
				case schema.AudioTransformCtrlSessionUpdate:
					_ = stream.Send(buildConfigRequest(sampleFormat, &ctrl))
				case schema.AudioTransformCtrlSessionClose:
					_ = stream.CloseSend()
				}
			}
		}
		wg.Wait()
		return nil
	}
}

func parseSampleFormat(s string) (proto.AudioTransformStreamConfig_SampleFormat, error) {
	switch strings.ToUpper(s) {
	case schema.AudioTransformSampleFormatF32LE:
		return proto.AudioTransformStreamConfig_F32_LE, nil
	case schema.AudioTransformSampleFormatS16LE, "":
		return proto.AudioTransformStreamConfig_S16_LE, nil
	default:
		return 0, fmt.Errorf("unsupported sample_format: %q", s)
	}
}

func buildConfigRequest(fmt_ proto.AudioTransformStreamConfig_SampleFormat, ctrl *schema.AudioTransformStreamControl) *proto.AudioTransformFrameRequest {
	return &proto.AudioTransformFrameRequest{
		Payload: &proto.AudioTransformFrameRequest_Config{
			Config: &proto.AudioTransformStreamConfig{
				SampleFormat: fmt_,
				SampleRate:   int32(ctrl.SampleRate),
				FrameSamples: int32(ctrl.FrameSamples),
				Params:       ctrl.Params,
				Reset_:       ctrl.Reset,
			},
		},
	}
}

// exposedAudioTransformHeaders is the CORS allow-list for the artifact headers
// this endpoint sets. A browser cannot read any of them without it.
const exposedAudioTransformHeaders = "X-Audio-Input-Url, X-Audio-Reference-Url, X-Audio-Stems"

// convertStems applies the request's sample_rate and response_format to every
// stem, so the whole set stays in the shape the caller asked for and dst keeps
// duplicating the stem it was selected from. Both conversions are no-ops when
// unset, so an ordinary request pays nothing.
//
// A stem whose conversion fails is DROPPED from the list rather than reported
// at its original rate or format: advertising a URL whose file is not in the
// shape the caller asked for is the silent wrong answer this whole design is
// trying to avoid, and the caller still receives the audio it actually asked
// for in the body. The failure is logged, since the file is on disk either way.
func convertStems(stems []backend.AudioTransformStem, sampleRate int, format string) []backend.AudioTransformStem {
	if len(stems) == 0 {
		return nil
	}
	converted := make([]backend.AudioTransformStem, 0, len(stems))
	for _, stem := range stems {
		path := stem.Dst
		var err error
		if sampleRate > 0 {
			path, err = utils.AudioResample(path, sampleRate)
			if err != nil {
				xlog.Warn("audio_transform: cannot resample stem", "stem", stem.Name, "error", err)
				continue
			}
		}
		path, err = utils.AudioConvert(path, format)
		if err != nil {
			xlog.Warn("audio_transform: cannot convert stem", "stem", stem.Name, "error", err)
			continue
		}
		converted = append(converted, backend.AudioTransformStem{Name: stem.Name, Dst: path})
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

// stemsHeader renders the extra named outputs as a compact JSON array for the
// X-Audio-Stems response header:
//
//	[{"name":"vocals","url":"/generated-audio/transform-1.vocals.wav"}, ...]
//
// JSON rather than a name=url list because a stem name is the MODEL'S string:
// it comes out of the checkpoint's config, so a name containing a comma or an
// equals sign would silently corrupt any hand-rolled separator format. Header
// values must also stay on one line, which json.Marshal guarantees since it
// escapes every control character.
func stemsHeader(stems []backend.AudioTransformStem) string {
	if len(stems) == 0 {
		return ""
	}
	type stemEntry struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	entries := make([]stemEntry, 0, len(stems))
	for _, stem := range stems {
		name := filepath.Base(stem.Dst)
		if name == "" || name == "." || name == string(filepath.Separator) {
			continue
		}
		// PathEscape, because the file name carries the MODEL'S stem name and a
		// stem name legally contains a space (there is a spec for "lead
		// vocals"), and '#', '?' and '%' are legal too. An unescaped '#' would
		// truncate the URL in the client before it ever reached the server.
		// The `name` field keeps the raw stem name; only the URL is escaped.
		entries = append(entries, stemEntry{
			Name: stem.Name,
			URL:  "/generated-audio/" + url.PathEscape(name),
		})
	}
	if len(entries) == 0 {
		return ""
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		// Unreachable for a slice of plain strings, and a failure here must
		// not cost the caller the audio it did ask for.
		xlog.Debug("audio_transform: cannot encode stem header", "error", err)
		return ""
	}
	return string(encoded)
}

// saveMultipartFileAsWAV materialises an uploaded multipart file into `dir` as
// a 16-bit PCM WAV. `name` is used as the base filename for the converted
// output so the dir stays readable for debugging (e.g. "audio.wav",
// "reference.wav").
//
// `foldToMono16k` picks the target shape. True folds to 16 kHz mono, which is
// what LocalVQE requires; false keeps the upload's own rate and channel count,
// which is what everything else needs and what source separation cannot work
// without. Either way a WAV that already matches is passed through rather than
// re-encoded.
func saveMultipartFileAsWAV(fh *multipart.FileHeader, dir, name string, foldToMono16k bool) (string, error) {
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	// `name` prefixes the raw copy too, and that is not cosmetic. Both parts of
	// one request land in the SAME dir, and a client that uploads
	// `-F audio=@mic/clip.wav -F reference=@loopback/clip.wav` sends the same
	// BASENAME twice. Without the prefix both parts write "raw-clip.wav", and
	// because utils.AudioToWavPreservingShape hardlinks a WAV that is already
	// PCM16 rather than copying it, audio.wav and raw-clip.wav are one inode:
	// the reference part's os.Create then truncates the audio the caller
	// uploaded first and refills it with the reference. The request still
	// returns 200, with mic == reference, which for AEC means the canceller
	// nulls everything and hands back near-silence.
	raw := filepath.Join(dir, name+"-raw-"+path.Base(fh.Filename))
	out, err := os.Create(raw)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, f); err != nil {
		_ = out.Close()
		return "", err
	}
	_ = out.Close()

	dst := filepath.Join(dir, name+".wav")
	convert := utils.AudioToWavPreservingShape
	if foldToMono16k {
		convert = utils.AudioToWav
	}
	if err := convert(raw, dst); err != nil {
		return "", fmt.Errorf("normalize %s: %w", name, err)
	}
	return dst, nil
}

// collectParamsFromForm walks the multipart form values and harvests any
// that match the `params[<key>]` shape. Returns nil if there are no matches.
func collectParamsFromForm(c echo.Context) map[string]string {
	params := map[string]string{}
	form, err := c.FormParams()
	if err != nil {
		return params
	}
	for key, vals := range form {
		if len(vals) == 0 {
			continue
		}
		if !strings.HasPrefix(key, "params[") || !strings.HasSuffix(key, "]") {
			continue
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(key, "params["), "]")
		inner = strings.TrimSpace(inner)
		if inner == "" {
			continue
		}
		// Last value wins for duplicate keys — matches OpenAI's form-field
		// override semantics.
		params[inner] = vals[len(vals)-1]
	}
	// Form-field shortcuts for the common LocalVQE knobs. params[*] still wins
	// when both are provided (they ran first).
	if _, exists := params[schema.AudioTransformParamNoiseGate]; !exists {
		if v := c.FormValue(schema.AudioTransformParamNoiseGate); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				if b {
					params[schema.AudioTransformParamNoiseGate] = "true"
				} else {
					params[schema.AudioTransformParamNoiseGate] = "false"
				}
			}
		}
	}
	if _, exists := params[schema.AudioTransformParamNoiseGateThreshold]; !exists {
		if v := c.FormValue(schema.AudioTransformParamNoiseGateThreshold); v != "" {
			params[schema.AudioTransformParamNoiseGateThreshold] = v
		}
	}
	return params
}

// splitStereoFrameInto deinterleaves a stereo PCM frame in-place into
// caller-owned reusable buffers (channel 0 → audio, channel 1 → reference).
// Sample size is inferred from the proto enum: s16=2 B, f32=4 B. Trailing
// odd bytes are truncated.
func splitStereoFrameInto(buf []byte, fmt_ proto.AudioTransformStreamConfig_SampleFormat, audio, ref *[]byte) ([]byte, []byte) {
	sampleSize := 2
	if fmt_ == proto.AudioTransformStreamConfig_F32_LE {
		sampleSize = 4
	}
	stride := sampleSize * 2
	n := len(buf) / stride
	want := n * sampleSize
	if cap(*audio) < want {
		*audio = make([]byte, want)
	} else {
		*audio = (*audio)[:want]
	}
	if cap(*ref) < want {
		*ref = make([]byte, want)
	} else {
		*ref = (*ref)[:want]
	}
	for i := 0; i < n; i++ {
		copy((*audio)[i*sampleSize:(i+1)*sampleSize], buf[i*stride:i*stride+sampleSize])
		copy((*ref)[i*sampleSize:(i+1)*sampleSize], buf[i*stride+sampleSize:(i+1)*stride])
	}
	return *audio, *ref
}

func sendWSError(ws *lockedConn, msg string) {
	payload, _ := json.Marshal(schema.AudioTransformStreamControl{
		Type:  schema.AudioTransformCtrlError,
		Error: msg,
	})
	_ = ws.WriteMessage(websocket.TextMessage, payload)
}
