package localaitools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of transform_audio. The sample rate bounds repeat the ones of the
// route (a request outside them is a 400 there); checking here tells the model
// before a recording is read and uploaded.
const (
	minTransformSampleRate = 8000
	maxTransformSampleRate = 192000
	maxTransformStems      = 16
	maxTransformText       = 2000
	maxTransformWord       = 64
	generatedAudioPrefix   = "/generated-audio/"
)

// transformParamNames are the backend parameters a call may set, with the
// kind that validates each value. The route forwards params to the backend
// as they come, so this list is the whole of what a model may pass: a key
// that is not here is refused, and every value is a short scalar, never a
// path or a URL.
var transformParamNames = map[string]string{
	"noise_gate":                "bool",
	"noise_gate_threshold_dbfs": "dbfs",
	"stem":                      "word",
	"language":                  "word",
	"text":                      "text",
	"target_text":               "text",
}

var transformWord = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _.-]*$`)

// checkTransformParams validates a params map against transformParamNames
// and returns the clean copy to send.
func checkTransformParams(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for k, v := range in {
		kind, ok := transformParamNames[k]
		if !ok {
			names := make([]string, 0, len(transformParamNames))
			for n := range transformParamNames {
				names = append(names, n)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("params: %q is not allowed (use %s)", k, strings.Join(names, ", "))
		}
		v = strings.TrimSpace(v)
		switch kind {
		case "bool":
			if v != "true" && v != "false" {
				return nil, fmt.Errorf("params: %s must be true or false", k)
			}
		case "dbfs":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < -120 || f > 0 {
				return nil, fmt.Errorf("params: %s must be a number from -120 to 0", k)
			}
		case "word":
			if len(v) > maxTransformWord || !transformWord.MatchString(v) || strings.Contains(v, "..") {
				return nil, fmt.Errorf("params: %s must be a short name of letters, digits, spaces, dots, dashes and underscores, not a path", k)
			}
		case "text":
			if v == "" || len(v) > maxTransformText || !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return nil, fmt.Errorf("params: %s must be plain text of at most %d bytes", k, maxTransformText)
			}
		}
		out[k] = v
	}
	return out, nil
}

// TransformAudio runs an audio-in, audio-out transform (POST
// /audio/transformations): echo cancellation and noise suppression, source
// separation, voice conversion. The body of the answer is one recording; a
// model with several outputs (stems) names the others in the X-Audio-Stems
// header, and each one is fetched and stored too.
func (i *Inference) TransformAudio(ctx context.Context, req TransformAudioRequest) (*TransformAudioResult, error) {
	spec := clientSpec(ToolTransformAudio)
	if req.SampleRate != 0 && (req.SampleRate < minTransformSampleRate || req.SampleRate > maxTransformSampleRate) {
		return nil, fmt.Errorf("sample_rate must be from %d to %d Hz, or left out", minTransformSampleRate, maxTransformSampleRate)
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format != "" && !slices.Contains([]string{"wav", "mp3", "ogg", "flac"}, format) {
		return nil, errors.New("response_format must be wav, mp3, ogg or flac")
	}
	params, err := checkTransformParams(req.Params)
	if err != nil {
		return nil, err
	}
	in, err := i.readAudio(ctx, req.Audio, "audio")
	if err != nil {
		return nil, err
	}
	var ref File
	if strings.TrimSpace(req.Reference) != "" {
		if ref, err = i.readAudio(ctx, req.Reference, "reference"); err != nil {
			return nil, err
		}
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	release, err := i.gate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	m := newMultipart()
	m.field("model", model)
	m.field("response_format", format)
	if req.SampleRate > 0 {
		m.field("sample_rate", strconv.Itoa(req.SampleRate))
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m.field("params["+k+"]", params[k])
	}
	if err := m.file("audio", in, "audio.wav"); err != nil {
		return nil, err
	}
	in.Data = nil
	if ref.Data != nil {
		if err := m.file("reference", ref, "reference.wav"); err != nil {
			return nil, err
		}
		ref.Data = nil
	}
	body, ct, err := m.finish()
	if err != nil {
		return nil, err
	}
	data, hdr, err := i.send(ctx, spec, model, spec.Route, ct, body)
	if err != nil {
		return nil, err
	}
	main, err := i.store(ctx, "transformed", data, audioMIME(hdr, data))
	if err != nil {
		return nil, err
	}
	res := &TransformAudioResult{Model: model, Audio: main}
	stems := parseStemsHeader(hdr.Get("X-Audio-Stems"))
	failed := 0
	for _, s := range stems {
		raw, shdr, err := i.fetchStem(ctx, spec, model, s.url)
		if err != nil {
			failed++
			continue
		}
		if bytes.Equal(raw, data) {
			// The body is one of the stems: name it, do not store it twice.
			res.Stems = append(res.Stems, AudioStem{Name: s.name, Audio: main})
			continue
		}
		f, err := i.store(ctx, "stem-"+safeFileWord(s.name), raw, audioMIME(shdr, raw))
		if err != nil {
			failed++
			continue
		}
		res.Stems = append(res.Stems, AudioStem{Name: s.name, Audio: f})
	}
	if failed > 0 {
		res.Note = fmt.Sprintf("%d of %d stems could not be fetched.", failed, len(stems))
	}
	return res, nil
}

type stemRef struct{ name, url string }

// parseStemsHeader reads the X-Audio-Stems header: a JSON array of name and
// URL. It keeps only entries whose URL is a file of the generated-audio
// folder, so a header cannot make the tool fetch anything else, and at most
// maxTransformStems of them.
func parseStemsHeader(h string) []stemRef {
	if strings.TrimSpace(h) == "" {
		return nil
	}
	var entries []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if json.Unmarshal([]byte(h), &entries) != nil {
		return nil
	}
	var out []stemRef
	for _, e := range entries {
		u, err := url.Parse(e.URL)
		if err != nil || u.Host != "" || u.RawQuery != "" || !strings.HasPrefix(u.Path, generatedAudioPrefix) ||
			path.Clean(u.Path) != u.Path || strings.Contains(u.Path, "..") {
			continue
		}
		out = append(out, stemRef{name: e.Name, url: u.EscapedPath()})
		if len(out) == maxTransformStems {
			break
		}
	}
	return out
}

func (i *Inference) fetchStem(ctx context.Context, spec ClientToolSpec, model, escapedPath string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PlaceholderHost+escapedPath, nil)
	if err != nil {
		return nil, nil, err
	}
	return i.roundTrip(req, spec, model)
}

// safeFileWord keeps the letters, digits, dashes and underscores of a stem
// name, for a file name.
func safeFileWord(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "out"
	}
	return b.String()
}
