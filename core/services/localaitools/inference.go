package localaitools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register the decoders that image dimensions use
	_ "image/jpeg" //
	_ "image/png"  //
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
)

// PlaceholderHost is the host of the requests that Inference builds. The
// function that sends them replaces it: the in-process client answers every
// request in memory, the REST client points them at the remote server.
const PlaceholderHost = "http://localai.invalid"

// maxAnswerBytes bounds what one model answer may weigh in memory.
const maxAnswerBytes = 256 << 20

// ModelChooser names the model a client tool uses when the caller names none.
type ModelChooser interface {
	// FirstModel returns the first installed model, by name, that has the use
	// case, or "" when there is none.
	FirstModel(ctx context.Context, use config.ModelConfigUsecase) (string, error)
}

// NoModelError means no installed model has the use case a tool needs.
type NoModelError struct{ Capability string }

func (e *NoModelError) Error() string {
	return fmt.Sprintf("no %s model installed. Install one from the model gallery, or ask an administrator to.", e.Capability)
}

// StatusError is an error answer of the API to a client tool.
type StatusError struct {
	Capability string
	Model      string
	Status     int
	Detail     string
}

func (e *StatusError) Error() string {
	switch e.Status {
	case http.StatusForbidden:
		return fmt.Sprintf("this account may not use %s: the server refused the request (403). "+
			"The account has no permission for it, or it may not use the model %q. Tell the person; an administrator can allow it.", e.Capability, e.Model)
	case http.StatusUnauthorized:
		return fmt.Sprintf("the server did not accept the credentials for %s (401)", e.Capability)
	case http.StatusTooManyRequests:
		return fmt.Sprintf("the server refused the %s request: the quota is used up or the server is busy (429)", e.Capability)
	case http.StatusNotFound:
		return fmt.Sprintf("the server does not know the model %q for %s (404). Check the name with list_installed_models.", e.Model, e.Capability)
	}
	msg := fmt.Sprintf("%s failed with status %d", e.Capability, e.Status)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// Inference runs the client tools against the LocalAI API. Both
// LocalAIClient implementations use it, so a request is built and an answer
// read in one place: only the way a request is sent differs.
type Inference struct {
	// Do sends a request as the acting user. The URL of a request is
	// PlaceholderHost plus the route.
	Do func(req *http.Request) (*http.Response, error)
	// Models chooses a model when the caller names none.
	Models ModelChooser
	// Vision chooses the model of describe_image. guess is true when the model
	// may not take images and is tried anyway. Nil: Models picks a model with
	// the vision use case.
	Vision func(ctx context.Context) (model string, guess bool)
	// Files reads input files and stores results.
	Files FileStore
}

// gate takes one of the heavy-call places of the acting user. Without
// accounts every call is the same user. The clients build an Inference for
// each call, so the count lives in the package (heavyCalls).
func (i *Inference) gate(ctx context.Context) (func(), error) {
	user := ""
	if u := UserFromContext(ctx); u != nil {
		user = u.ID
	}
	return heavyCalls.enter(user)
}

func (i *Inference) files(ctx context.Context) (FileStore, error) {
	if fs := filesFromContext(ctx); fs != nil {
		return fs, nil
	}
	if i == nil || i.Files == nil {
		return nil, errors.New("file input and output are not available here: no file store is configured")
	}
	return i.Files, nil
}

func (i *Inference) pick(ctx context.Context, given string, spec ClientToolSpec) (string, error) {
	if given = strings.TrimSpace(given); given != "" {
		return given, nil
	}
	if i == nil || i.Models == nil {
		return "", errors.New("cannot choose a model here: name one with the model argument")
	}
	m, err := i.Models.FirstModel(ctx, spec.Flag)
	if err != nil {
		return "", err
	}
	if m == "" {
		return "", &NoModelError{Capability: spec.Capability}
	}
	return m, nil
}

func (i *Inference) read(ctx context.Context, ref string, max int64, what string) (File, error) {
	fs, err := i.files(ctx)
	if err != nil {
		return File{}, err
	}
	if strings.TrimSpace(ref) == "" {
		return File{}, fmt.Errorf("%s is required", what)
	}
	f, err := fs.Read(ctx, strings.TrimSpace(ref), max)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrFileTooLarge) {
			return File{}, fmt.Errorf("%s: %w", what, err)
		}
		return File{}, fmt.Errorf("read %s: %w", what, err)
	}
	return f, nil
}

func (i *Inference) readImage(ctx context.Context, ref, what string) (File, error) {
	f, err := i.read(ctx, ref, MaxImageBytes, what)
	if err != nil {
		return f, err
	}
	if !strings.HasPrefix(http.DetectContentType(f.Data), "image/") {
		return f, &WrongKindError{What: what, Kind: "an image"}
	}
	f.MIME = http.DetectContentType(f.Data)
	return f, nil
}

func (i *Inference) readAudio(ctx context.Context, ref, what string) (File, error) {
	f, err := i.read(ctx, ref, MaxAudioBytes, what)
	if err != nil {
		return f, err
	}
	if ct := http.DetectContentType(f.Data); strings.HasPrefix(ct, "audio/") || strings.HasPrefix(f.MIME, "audio/") || extForMIME(f.MIME) != "" || IsAudioName(f.Name) {
		return f, nil
	}
	return f, &WrongKindError{What: what, Kind: "audio"}
}

// IsAudioName tells whether a file name has the extension of a recording. The
// chats use it too, to decide which attachments to transcribe.
func IsAudioName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".wav", ".mp3", ".m4a", ".ogg", ".oga", ".opus", ".flac", ".webm", ".aac", ".weba":
		return true
	}
	return false
}

// store writes a result file and describes it.
func (i *Inference) store(ctx context.Context, name string, data []byte, mimeType string) (OutputFile, error) {
	fs, err := i.files(ctx)
	if err != nil {
		return OutputFile{}, err
	}
	if int64(len(data)) > maxResultBytes {
		return OutputFile{}, fmt.Errorf("store the result: %w: %d bytes, the limit is %d", ErrFileTooLarge, len(data), maxResultBytes)
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	mimeType, _, _ = strings.Cut(mimeType, ";")
	if path.Ext(name) == "" {
		name += extForMIME(mimeType)
	}
	ref, err := fs.Write(ctx, name, data, mimeType)
	if err != nil {
		return OutputFile{}, fmt.Errorf("store the result: %w", err)
	}
	out := OutputFile{Ref: ref, MIME: mimeType, Size: len(data)}
	if strings.HasPrefix(mimeType, "image/") {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			out.Width, out.Height = cfg.Width, cfg.Height
		}
	}
	return out, nil
}

// send posts a body to a route and returns the answer body and headers. A
// status outside 2xx is a *StatusError.
func (i *Inference) send(ctx context.Context, spec ClientToolSpec, model, route, contentType string, body []byte) ([]byte, http.Header, error) {
	if i == nil || i.Do == nil {
		return nil, nil, errors.New("inference is not available on this server")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, PlaceholderHost+route, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return i.roundTrip(req, spec, model)
}

func (i *Inference) roundTrip(req *http.Request, spec ClientToolSpec, model string) ([]byte, http.Header, error) {
	resp, err := i.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil, &StatusError{Capability: spec.Capability, Model: model, Status: resp.StatusCode, Detail: upstreamMessage(data)}
	}
	return data, resp.Header, nil
}

func (i *Inference) postJSON(ctx context.Context, spec ClientToolSpec, model string, body any) ([]byte, http.Header, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	return i.send(ctx, spec, model, spec.Route, "application/json", raw)
}

// upstreamMessage returns the message of an error body as one short line.
func upstreamMessage(body []byte) string {
	var out struct {
		Error json.RawMessage `json:"error"`
	}
	text := string(body)
	if json.Unmarshal(body, &out) == nil && len(out.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(out.Error, &obj) == nil && obj.Message != "":
			text = obj.Message
		case json.Unmarshal(out.Error, &s) == nil && s != "":
			text = s
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text
}

type multipartBody struct {
	buf bytes.Buffer
	w   *multipart.Writer
}

func newMultipart() *multipartBody {
	m := &multipartBody{}
	m.w = multipart.NewWriter(&m.buf)
	return m
}

func (m *multipartBody) field(name, value string) {
	if value != "" {
		_ = m.w.WriteField(name, value)
	}
}

func (m *multipartBody) file(field string, f File, fallbackName string) error {
	name := f.Name
	if name == "" || name == "data" {
		name = fallbackName
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": path.Base(name)}))
	ct := f.MIME
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	fw, err := m.w.CreatePart(h)
	if err != nil {
		return err
	}
	_, err = fw.Write(f.Data)
	return err
}

func (m *multipartBody) finish() ([]byte, string, error) {
	if err := m.w.Close(); err != nil {
		return nil, "", err
	}
	return m.buf.Bytes(), m.w.FormDataContentType(), nil
}

// GenerateImage makes images from a prompt (POST /v1/images/generations).
func (i *Inference) GenerateImage(ctx context.Context, req GenerateImageRequest) (*ImageResult, error) {
	spec := clientSpec(ToolGenerateImage)
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	n := req.N
	if n == 0 {
		n = 1
	}
	if n < 1 || n > 4 {
		return nil, errors.New("n must be from 1 to 4")
	}
	if err := checkSteps(req.Steps); err != nil {
		return nil, err
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
	body := map[string]any{"model": model, "prompt": req.Prompt, "n": n, "response_format": "b64_json"}
	if req.Size != "" {
		body["size"] = req.Size
	}
	if req.NegativePrompt != "" {
		body["negative_prompt"] = req.NegativePrompt
	}
	if req.Steps > 0 {
		body["step"] = req.Steps
	}
	data, _, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Data) == 0 {
		return nil, errors.New("the model returned no image")
	}
	res := &ImageResult{Model: model}
	for _, it := range out.Data {
		raw, err := base64.StdEncoding.DecodeString(it.B64JSON)
		if err != nil || len(raw) == 0 {
			return nil, errors.New("the model returned an image that cannot be read")
		}
		f, err := i.store(ctx, "image", raw, "")
		if err != nil {
			return nil, err
		}
		res.Images = append(res.Images, f)
	}
	return res, nil
}

// EditImage repaints the masked part of an image (POST /v1/images/inpainting).
func (i *Inference) EditImage(ctx context.Context, req EditImageRequest) (*ImageResult, error) {
	spec := clientSpecAction(ToolGenerateImage, ActionEdit)
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	if err := checkSteps(req.Steps); err != nil {
		return nil, err
	}
	release, err := i.gate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	img, err := i.readImage(ctx, req.Image, "image")
	if err != nil {
		return nil, err
	}
	mask, err := i.readImage(ctx, req.Mask, "mask")
	if err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	m := newMultipart()
	m.field("model", model)
	m.field("prompt", req.Prompt)
	if req.Steps > 0 {
		m.field("steps", strconv.Itoa(req.Steps))
	}
	if err := m.file("image", img, "image.png"); err != nil {
		return nil, err
	}
	if err := m.file("mask", mask, "mask.png"); err != nil {
		return nil, err
	}
	body, ct, err := m.finish()
	if err != nil {
		return nil, err
	}
	data, _, err := i.send(ctx, spec, model, spec.Route, ct, body)
	if err != nil {
		return nil, err
	}
	// The route answers with the address of the result, not the bytes.
	var out struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Data) == 0 {
		return nil, errors.New("the model returned no image")
	}
	raw, err := i.fetchResult(ctx, spec, model, out.Data[0].URL, out.Data[0].B64JSON)
	if err != nil {
		return nil, err
	}
	f, err := i.store(ctx, "edited", raw, "")
	if err != nil {
		return nil, err
	}
	return &ImageResult{Model: model, Images: []OutputFile{f}}, nil
}

// fetchResult returns the bytes of an item of an OpenAI-style answer: the
// inline base64 when there is one, otherwise the file at the URL, fetched
// through the same sender so the same credentials and rights apply.
func (i *Inference) fetchResult(ctx context.Context, spec ClientToolSpec, model, rawURL, b64 string) ([]byte, error) {
	if b64 != "" {
		return base64.StdEncoding.DecodeString(b64)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return nil, errors.New("the model returned no result file")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PlaceholderHost+u.EscapedPath(), nil)
	if err != nil {
		return nil, err
	}
	data, _, err := i.roundTrip(req, spec, model)
	return data, err
}

// Speak turns text into speech (POST /v1/audio/speech).
func (i *Inference) Speak(ctx context.Context, req SpeakRequest) (*AudioResult, error) {
	spec := clientSpec(ToolSpeak)
	if strings.TrimSpace(req.Text) == "" {
		return nil, errors.New("text is required")
	}
	if u := UserFromContext(ctx); u != nil && u.Role != auth.RoleAdmin && !voiceprofile.IsReference(req.Voice) && voiceprofile.UnsafeVoice(req.Voice) {
		// The TTS API refuses it too; refusing here tells the model why.
		return nil, errors.New("speak: voice must be a voice of the model or a voice from voice_profile, not a file path")
	}
	if req.Speed != nil && (*req.Speed < 0.25 || *req.Speed > 4) {
		return nil, errors.New("speed must be from 0.25 to 4.0")
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
	body := map[string]any{"model": model, "input": req.Text}
	if req.Voice != "" {
		body["voice"] = req.Voice
	}
	if req.Language != "" {
		body["language"] = req.Language
	}
	if req.Format != "" {
		body["response_format"] = req.Format
	}
	if req.Speed != nil {
		body["speed"] = *req.Speed
	}
	if req.Instructions != "" {
		body["instructions"] = req.Instructions
	}
	data, hdr, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound && voiceprofile.IsReference(req.Voice) {
			// The model exists: it is the profile that this account cannot see.
			return nil, errors.New("speak: no such voice profile (404). voice_profile with action list shows the voices of this account")
		}
		return nil, err
	}
	f, err := i.store(ctx, "speech", data, audioMIME(hdr, data))
	if err != nil {
		return nil, err
	}
	return &AudioResult{Model: model, Audio: f}, nil
}

// audioMIME returns the media type of an audio answer.
func audioMIME(h http.Header, data []byte) string {
	ct, _, _ := strings.Cut(h.Get("Content-Type"), ";")
	ct = strings.TrimSpace(ct)
	if strings.HasPrefix(ct, "audio/") {
		return ct
	}
	if ct = http.DetectContentType(data); strings.HasPrefix(ct, "audio/") {
		return ct
	}
	return "audio/wav"
}

// TranscribeAudio turns a recording into text (POST /v1/audio/transcriptions).
func (i *Inference) TranscribeAudio(ctx context.Context, req TranscribeAudioRequest) (*TranscriptResult, error) {
	spec := clientSpec(ToolTranscribeAudio)
	audio, err := i.readAudio(ctx, req.Audio, "audio")
	if err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	m := newMultipart()
	m.field("model", model)
	m.field("language", req.Language)
	m.field("prompt", req.Prompt)
	m.field("response_format", "json")
	audio.MIME = "application/octet-stream"
	if err := m.file("file", audio, "audio.wav"); err != nil {
		return nil, err
	}
	body, ct, err := m.finish()
	if err != nil {
		return nil, err
	}
	data, _, err := i.send(ctx, spec, model, spec.Route, ct, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("the model gave no transcript")
	}
	return &TranscriptResult{Model: model, Text: strings.TrimSpace(out.Text), Language: out.Language, Duration: out.Duration}, nil
}

// DescribeImage asks a vision model about an image, as a chat completion with
// an image part.
func (i *Inference) DescribeImage(ctx context.Context, req DescribeImageRequest) (*DescribeImageResult, error) {
	spec := clientSpec(ToolDescribeImage)
	img, err := i.readImage(ctx, req.Image, "image")
	if err != nil {
		return nil, err
	}
	model, guess := strings.TrimSpace(req.Model), false
	if model == "" {
		if i != nil && i.Vision != nil {
			model, guess = i.Vision(ctx)
			if model == "" {
				return nil, &NoModelError{Capability: spec.Capability}
			}
		} else if model, err = i.pick(ctx, "", spec); err != nil {
			return nil, err
		}
	}
	question := req.Question
	if strings.TrimSpace(question) == "" {
		question = "Describe this image in detail. Transcribe any text that you can read in it."
	}
	body := map[string]any{
		"model": model,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": question},
			map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURL(img)}},
		}}},
	}
	data, _, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		var se *StatusError
		if guess && errors.As(err, &se) && se.Status != http.StatusForbidden && se.Status != http.StatusTooManyRequests {
			return nil, fmt.Errorf("the model %s did not accept the image (%w). If it can look at images, turn on \"This model can look at images\" in its settings; otherwise name another model", model, err)
		}
		return nil, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		return nil, errors.New("the model gave no description")
	}
	return &DescribeImageResult{Model: model, Description: strings.TrimSpace(out.Choices[0].Message.Content)}, nil
}

// Embed turns texts into vectors (POST /v1/embeddings). The vectors are
// stored as a JSON file.
func (i *Inference) Embed(ctx context.Context, req EmbedRequest) (*EmbedResult, error) {
	spec := clientSpec(ToolEmbed)
	if len(req.Input) == 0 {
		return nil, errors.New("input is required")
	}
	if err := checkTexts("input", req.Input, maxEmbedInputs); err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	data, _, err := i.postJSON(ctx, spec, model, map[string]any{"model": model, "input": req.Input})
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Data) == 0 {
		return nil, errors.New("the model returned no embedding")
	}
	vectors := make([][]float32, 0, len(out.Data))
	preview := make([][]float32, 0, len(out.Data))
	for _, d := range out.Data {
		vectors = append(vectors, d.Embedding)
		preview = append(preview, d.Embedding[:min(len(d.Embedding), 8)])
	}
	raw, err := json.Marshal(map[string]any{"model": model, "input": req.Input, "vectors": vectors})
	if err != nil {
		return nil, err
	}
	f, err := i.store(ctx, "embeddings.json", raw, "application/json")
	if err != nil {
		return nil, err
	}
	return &EmbedResult{Model: model, Count: len(vectors), Dimensions: len(vectors[0]), Vectors: f, Preview: preview}, nil
}

// Detect finds objects in an image (POST /v1/detection).
func (i *Inference) Detect(ctx context.Context, req DetectRequest) (*DetectResult, error) {
	spec := clientSpec(ToolDetect)
	img, err := i.readImage(ctx, req.Image, "image")
	if err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": model, "image": dataURL(img)}
	if req.Prompt != "" {
		body["prompt"] = req.Prompt
	}
	if req.Threshold > 0 {
		body["threshold"] = req.Threshold
	}
	data, _, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Detections []struct {
			Detection
			Mask string `json:"mask"`
		} `json:"detections"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("the model returned no detections")
	}
	res := &DetectResult{Model: model, Detections: []Detection{}}
	for _, d := range out.Detections {
		d.Detection.HasMask = d.Mask != ""
		res.Detections = append(res.Detections, d.Detection)
	}
	return res, nil
}

// Rerank ranks documents against a query (POST /v1/rerank).
func (i *Inference) Rerank(ctx context.Context, req RerankRequest) (*RerankResult, error) {
	spec := clientSpec(ToolRerank)
	if strings.TrimSpace(req.Query) == "" || len(req.Documents) == 0 {
		return nil, errors.New("query and documents are required")
	}
	if err := checkTexts("documents", req.Documents, maxRerankDocs); err != nil {
		return nil, err
	}
	if req.TopN < 0 || req.TopN > maxRerankDocs {
		return nil, fmt.Errorf("top_n must be from 1 to %d", maxRerankDocs)
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": model, "query": req.Query, "documents": req.Documents}
	if req.TopN > 0 {
		body["top_n"] = req.TopN
	}
	data, _, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results []struct {
			Index    int     `json:"index"`
			Score    float64 `json:"relevance_score"`
			Document struct {
				Text string `json:"text"`
			} `json:"document"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("the model returned no ranking")
	}
	res := &RerankResult{Model: model, Results: []RerankItem{}}
	for _, r := range out.Results {
		res.Results = append(res.Results, RerankItem{Index: r.Index, Score: r.Score, Text: r.Document.Text})
	}
	return res, nil
}

// GenerateVideo makes a video from a prompt (POST /video).
func (i *Inference) GenerateVideo(ctx context.Context, req GenerateVideoRequest) (*VideoResult, error) {
	spec := clientSpec(ToolGenerateVideo)
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	if err := checkVideoLength(req.Seconds, req.FPS); err != nil {
		return nil, err
	}
	release, err := i.gate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	body := map[string]any{"prompt": req.Prompt, "response_format": "b64_json"}
	if req.StartImage != "" {
		img, err := i.readImage(ctx, req.StartImage, "start_image")
		if err != nil {
			return nil, err
		}
		body["start_image"] = dataURL(img)
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	body["model"] = model
	if req.Size != "" {
		body["size"] = req.Size
	}
	if req.Seconds != "" {
		body["seconds"] = req.Seconds
	}
	if req.FPS > 0 {
		body["fps"] = req.FPS
	}
	if req.NegativePrompt != "" {
		body["negative_prompt"] = req.NegativePrompt
	}
	data, _, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Data) == 0 {
		return nil, errors.New("the model returned no video")
	}
	raw, err := i.fetchResult(ctx, spec, model, out.Data[0].URL, out.Data[0].B64JSON)
	if err != nil {
		return nil, err
	}
	f, err := i.store(ctx, "video", raw, "")
	if err != nil {
		return nil, err
	}
	return &VideoResult{Model: model, Video: f}, nil
}

// GenerateSound makes a sound or a piece of music (POST /v1/sound-generation).
func (i *Inference) GenerateSound(ctx context.Context, req GenerateSoundRequest) (*AudioResult, error) {
	spec := clientSpec(ToolGenerateSound)
	if strings.TrimSpace(req.Text) == "" {
		return nil, errors.New("text is required")
	}
	if err := checkSoundLength(req.Duration); err != nil {
		return nil, err
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
	body := map[string]any{"model_id": model, "text": req.Text}
	if req.Duration != nil {
		body["duration_seconds"] = *req.Duration
	}
	if req.Lyrics != "" {
		body["lyrics"] = req.Lyrics
	}
	if req.Instrumental != nil {
		body["instrumental"] = *req.Instrumental
	}
	data, hdr, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		return nil, err
	}
	f, err := i.store(ctx, "sound", data, audioMIME(hdr, data))
	if err != nil {
		return nil, err
	}
	return &AudioResult{Model: model, Audio: f}, nil
}

// VerifyVoice tells whether two recordings have the same speaker.
func (i *Inference) VerifyVoice(ctx context.Context, req VoiceVerifyRequest) (*VerifyResult, error) {
	spec := clientSpecAction(ToolVoice, ActionVerify)
	a1, err := i.readAudio(ctx, req.Audio1, "audio1")
	if err != nil {
		return nil, err
	}
	a2, err := i.readAudio(ctx, req.Audio2, "audio2")
	if err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	// Each clip is read once. Drop the raw bytes as soon as the data URL
	// holds them, so a pair of large clips is not kept three times.
	d1 := dataURL(a1)
	a1.Data = nil
	d2 := dataURL(a2)
	a2.Data = nil
	body := map[string]any{"model": model, "audio1": d1, "audio2": d2}
	if req.Threshold > 0 {
		body["threshold"] = req.Threshold
	}
	return i.verify(ctx, spec, model, body)
}

// VerifyFace tells whether two images show the same person.
func (i *Inference) VerifyFace(ctx context.Context, req VerifyFaceRequest) (*VerifyResult, error) {
	spec := clientSpecAction(ToolFace, ActionVerify)
	i1, err := i.readImage(ctx, req.Image1, "image1")
	if err != nil {
		return nil, err
	}
	i2, err := i.readImage(ctx, req.Image2, "image2")
	if err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	d1 := dataURL(i1)
	i1.Data = nil
	d2 := dataURL(i2)
	i2.Data = nil
	body := map[string]any{"model": model, "img1": d1, "img2": d2}
	if req.Threshold > 0 {
		body["threshold"] = req.Threshold
	}
	return i.verify(ctx, spec, model, body)
}

func (i *Inference) verify(ctx context.Context, spec ClientToolSpec, model string, body map[string]any) (*VerifyResult, error) {
	data, _, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		return nil, err
	}
	var out VerifyResult
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("the model returned no verdict")
	}
	if out.Model == "" {
		out.Model = model
	}
	return &out, nil
}

// IdentifyVoice finds the registered speakers closest to a recording.
func (i *Inference) IdentifyVoice(ctx context.Context, req IdentifyVoiceRequest) (*IdentifyResult, error) {
	spec := clientSpecAction(ToolVoice, ActionIdentify)
	a, err := i.readAudio(ctx, req.Audio, "audio")
	if err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	return i.identify(ctx, spec, model, map[string]any{"model": model, "audio": dataURL(a)}, req.TopK, req.Threshold)
}

// IdentifyFace finds the registered faces closest to the face in an image.
func (i *Inference) IdentifyFace(ctx context.Context, req IdentifyFaceRequest) (*IdentifyResult, error) {
	spec := clientSpecAction(ToolFace, ActionIdentify)
	img, err := i.readImage(ctx, req.Image, "image")
	if err != nil {
		return nil, err
	}
	model, err := i.pick(ctx, req.Model, spec)
	if err != nil {
		return nil, err
	}
	return i.identify(ctx, spec, model, map[string]any{"model": model, "img": dataURL(img)}, req.TopK, req.Threshold)
}

func (i *Inference) identify(ctx context.Context, spec ClientToolSpec, model string, body map[string]any, topK int, threshold float32) (*IdentifyResult, error) {
	if err := checkTopK(topK); err != nil {
		return nil, err
	}
	if topK > 0 {
		body["top_k"] = topK
	}
	if threshold > 0 {
		body["threshold"] = threshold
	}
	data, _, err := i.postJSON(ctx, spec, model, body)
	if err != nil {
		return nil, err
	}
	var out IdentifyResult
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("the model returned no matches")
	}
	out.Model = model
	if out.Matches == nil {
		out.Matches = []Match{}
	}
	return &out, nil
}
