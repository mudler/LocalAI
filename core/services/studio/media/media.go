// Package media gives the chats their eyes and ears. Two tools, describe_image
// and transcribe_audio, send a file of the user's workspace to a helper model
// through LocalAI's own API as that user, so the helper's allowlist and quota
// apply like for any request. A voice note attached to a message is
// transcribed on the way in. The helper models come from the instance
// settings, or are the first installed model with the capability.
package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Capability names a helper model's job.
type Capability string

// The helper jobs.
const (
	Vision        Capability = "vision"
	Transcription Capability = "transcription"
)

// Limits of what the tools send to a model.
const (
	maxImageBytes = 20 << 20
	maxAudioBytes = 100 << 20
	maxVoiceNote  = 25 << 20 // a voice note larger than this is kept but not transcribed
)

// baseURL is a placeholder: the provider's transport answers every request.
const baseURL = "http://studio.invalid/v1"

// UserLookup returns the user a request is made for, or nil when authentication is off.
type UserLookup func(ctx context.Context, principal string) (*auth.User, error)

// Resolver names the helper model for a job, or "" when none is installed.
type Resolver func(Capability) string

// Service runs the media tools.
type Service struct {
	p       *provider.Provider
	users   UserLookup
	resolve Resolver
}

// New returns a Service. users may be nil when authentication is off.
func New(p *provider.Provider, users UserLookup, resolve Resolver) *Service {
	return &Service{p: p, users: users, resolve: resolve}
}

func (s *Service) client(ctx context.Context, principal string) (*http.Client, error) {
	var u *auth.User
	if s.users != nil && principal != "" {
		var err error
		if u, err = s.users(ctx, principal); err != nil {
			return nil, fmt.Errorf("load the chat's user: %w", err)
		}
	}
	return s.p.Transport().Client(u), nil
}

func noModel(c Capability) error {
	switch c {
	case Vision:
		return errors.New("No model that can describe images is installed. Install one from the model gallery, or choose one in the settings under Chat image model.")
	}
	return errors.New("No model that can transcribe audio is installed. Install one from the model gallery, or choose one in the settings under Chat transcription model.")
}

// post sends a request and returns the body of a successful answer.
func (s *Service) post(ctx context.Context, principal, route, contentType string, body io.Reader, model string) ([]byte, error) {
	cl, err := s.client(ctx, principal)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+route, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := cl.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), session.MarkNotAllowed) {
			return nil, fmt.Errorf("you are not allowed to use the model %s", model)
		}
		if strings.Contains(err.Error(), session.MarkQuota) {
			return nil, errors.New("the model refused the request: your quota is used up or the server is busy")
		}
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("you are not allowed to use the model %s", model)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("the model refused the request: your quota is used up or the server is busy")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("the model %s answered with an error (%d)", model, resp.StatusCode)
	}
	return data, nil
}

// DescribeImage asks the vision model about an image.
func (s *Service) DescribeImage(ctx context.Context, principal, name string, img []byte, question string) (string, error) {
	model := s.resolve(Vision)
	if model == "" {
		return "", noModel(Vision)
	}
	ct := imageType(name, img)
	if ct == "" {
		return "", fmt.Errorf("%s is not an image", name)
	}
	if question == "" {
		question = "Describe this image in detail. Transcribe any text that you can read in it."
	}
	payload := map[string]any{
		"model": model,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": question},
			map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(img)}},
		}}},
	}
	raw, _ := json.Marshal(payload)
	data, err := s.post(ctx, principal, "/chat/completions", "application/json", bytes.NewReader(raw), model)
	if err != nil {
		return "", err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		return "", errors.New("the model gave no description")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// Transcribe turns an audio file into text with the transcription model.
func (s *Service) Transcribe(ctx context.Context, principal, name string, audio io.Reader) (string, error) {
	model := s.resolve(Transcription)
	if model == "" {
		return "", noModel(Transcription)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", model); err != nil {
		return "", err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": path.Base(name)}))
	h.Set("Content-Type", "application/octet-stream")
	fw, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, audio); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	data, err := s.post(ctx, principal, "/audio/transcriptions", mw.FormDataContentType(), &buf, model)
	if err != nil {
		return "", err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", errors.New("the model gave no transcript")
	}
	return strings.TrimSpace(out.Text), nil
}

// imageType returns the media type of an image, or "" when the bytes are not one.
func imageType(_ string, b []byte) string {
	sniffed := http.DetectContentType(b)
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	return ""
}

var audioExts = map[string]bool{".wav": true, ".mp3": true, ".m4a": true, ".ogg": true, ".oga": true, ".opus": true, ".flac": true, ".webm": true, ".aac": true, ".weba": true}

func isAudioName(name string) bool { return audioExts[strings.ToLower(path.Ext(name))] }

// IsAudio tells whether a file is a recording by its name, as PrepareUploads
// and transcribe_audio decide.
func IsAudio(name string) bool { return isAudioName(name) }

// CanTranscribe tells whether a transcription model is set in the settings
// or installed. It is false on a nil Service.
func (s *Service) CanTranscribe() bool { return s != nil && s.resolve(Transcription) != "" }

// PrepareUploads transcribes the voice notes among the files attached to a
// message and adds the transcripts to its text. The files themselves are kept,
// byte for byte. A voice note is skipped without a word when there is no
// transcription model or it is large, so the message still goes.
func (s *Service) PrepareUploads(ctx context.Context, principal, text string, up []session.Upload) (string, []session.Upload, error) {
	if len(up) == 0 || s.resolve(Transcription) == "" {
		return text, up, nil
	}
	out := make([]session.Upload, len(up))
	var notes strings.Builder
	for i, u := range up {
		out[i] = u
		if !isAudioName(u.Name) || u.Body == nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(u.Body, maxVoiceNote+1))
		if err != nil {
			return text, nil, err
		}
		if int64(len(data)) > maxVoiceNote {
			// Too large to transcribe here: keep it whole for the file tools.
			// Only the head is in memory; the rest of the body keeps streaming.
			out[i].Body = io.MultiReader(bytes.NewReader(data), u.Body)
			continue
		}
		out[i].Body = bytes.NewReader(data)
		transcript, err := s.Transcribe(ctx, principal, u.Name, bytes.NewReader(data))
		if err != nil || transcript == "" {
			continue // the file goes with the message either way
		}
		fmt.Fprintf(&notes, "\n\n[Voice note %s, transcribed: %s]", u.Name, transcript)
	}
	return text + notes.String(), out, nil
}

// Tools returns the media tools of one chat. They read files of the user's
// workspace through f and are not given to sub-agents.
func (s *Service) Tools(f *files.Service, a workspace.Actor) []session.Tool {
	if !f.Available() {
		return nil
	}
	read := func(ctx context.Context, rel string, max int64) (string, []byte, error) {
		abs, err := f.Abs(a.Principal, strings.TrimPrefix(rel, session.GuestRoot+"/"+session.GuestHome))
		if err != nil {
			return "", nil, errors.New("the path has a name that is not allowed")
		}
		r, n, err := f.Store().Open(ctx, a, abs)
		if err != nil {
			return "", nil, errors.New("not found")
		}
		defer r.Close()
		if n.Size > max {
			return "", nil, fmt.Errorf("%s is %d bytes, which is too large", n.Name, n.Size)
		}
		b, err := io.ReadAll(r)
		return n.Name, b, err
	}
	pathProp := `"path":{"type":"string","description":"A path in the user's files. / is their home folder."}`
	return []session.Tool{
		{Name: "describe_image", Description: "Describe an image in the user's files, or answer a question about it. Use it for pictures and screenshots, including to read text in them.",
			Schema: json.RawMessage(`{"type":"object","properties":{` + pathProp + `,"question":{"type":"string"}},"required":["path"]}`),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path, Question string }](raw)
				if err != nil {
					return "", err
				}
				if s.resolve(Vision) == "" {
					return "", noModel(Vision)
				}
				name, img, err := read(ctx, in.Path, maxImageBytes)
				if err != nil {
					return "", err
				}
				return s.DescribeImage(ctx, a.Principal, name, img, in.Question)
			}},
		{Name: "read_document", Description: "Read the text of a PDF, Word, Excel, PowerPoint or EPUB file in the user's files. Long documents come in parts: pass offset to read on.",
			Schema: json.RawMessage(`{"type":"object","properties":{` + pathProp + `,"offset":{"type":"integer","description":"Character to start at. 0 is the start."},"limit":{"type":"integer","description":"Most characters to return (default 20000)."}},"required":["path"]}`),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct {
					Path          string
					Offset, Limit int
				}](raw)
				if err != nil {
					return "", err
				}
				name, data, err := read(ctx, in.Path, maxDocumentBytes)
				if err != nil {
					return "", err
				}
				doc, err := DocumentText(ctx, name, data)
				if err != nil {
					return "", err
				}
				if doc == "" {
					return "", fmt.Errorf("%s has no text layer; it may be a scan", name)
				}
				limit := in.Limit
				if limit <= 0 || limit > 4*readDocumentChars {
					limit = readDocumentChars
				}
				part, more := cutChars(doc, max(in.Offset, 0), limit)
				total := utf8.RuneCountInString(doc)
				if part == "" {
					return "", fmt.Errorf("offset %d is past the end (%d characters)", in.Offset, total)
				}
				if more {
					part += fmt.Sprintf("\n[characters %d to %d of %d; call again with offset %d]", max(in.Offset, 0), max(in.Offset, 0)+utf8.RuneCountInString(part), total, max(in.Offset, 0)+utf8.RuneCountInString(part))
				}
				return part, nil
			}},
		{Name: "transcribe_audio", Description: "Turn a recording in the user's files into text.",
			Schema: json.RawMessage(`{"type":"object","properties":{` + pathProp + `},"required":["path"]}`),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path string }](raw)
				if err != nil {
					return "", err
				}
				if s.resolve(Transcription) == "" {
					return "", noModel(Transcription)
				}
				name, data, err := read(ctx, in.Path, maxAudioBytes)
				if err != nil {
					return "", err
				}
				if !isAudioName(name) && !strings.HasPrefix(http.DetectContentType(data), "audio/") {
					return "", fmt.Errorf("%s is not audio", name)
				}
				return s.Transcribe(ctx, a.Principal, name, bytes.NewReader(data))
			}},
	}
}
