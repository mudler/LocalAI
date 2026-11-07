// Package media gives the chats their eyes and ears. Two tools, describe_image
// and transcribe_audio, are aliases of the LocalAI client tools of the same
// name: they read a file of the chat's workspace, pick the helper model and
// call the client as the chat's user, so the helper's allowlist and quota
// apply like for any request. A voice note attached to a message is
// transcribed on the way in. The helper models come from the instance
// settings, or are the first installed model with the capability; a chat
// whose own model can look at images describes them with that model, and a
// cloud-proxy model whose abilities are unknown is tried as a last resort.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/extract"
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

// maxVoiceNote is the size over which a voice note is kept but not transcribed.
const maxVoiceNote = 25 << 20

// UserLookup returns the user a request is made for, or nil when authentication is off.
type UserLookup func(ctx context.Context, principal string) (*auth.User, error)

// Resolver names the helper model for a job, or "" when none is installed.
type Resolver func(Capability) string

// VisionChooser names the model that describe_image uses in a chat whose own
// model is chatModel, or "" when no model can look at images. guess is true
// when the model is the chat's own model and LocalAI cannot tell whether it
// accepts images (a cloud-proxy model with no vision declaration): the tool
// tries it and, when it refuses, tells the user what to do.
type VisionChooser func(chatModel string) (model string, guess bool)

// Client is the part of the LocalAI client that the media tools call. The
// in-process client of the assistant implements it.
type Client interface {
	DescribeImage(ctx context.Context, req localaitools.DescribeImageRequest) (*localaitools.DescribeImageResult, error)
	TranscribeAudio(ctx context.Context, req localaitools.TranscribeAudioRequest) (*localaitools.TranscriptResult, error)
}

// Service runs the media tools.
type Service struct {
	p       *provider.Provider
	users   UserLookup
	resolve Resolver
	vision  VisionChooser
	client  func() Client
}

// New returns a Service. users may be nil when authentication is off.
func New(p *provider.Provider, users UserLookup, resolve Resolver) *Service {
	return &Service{p: p, users: users, resolve: resolve}
}

// WithClient sets where the tools find the LocalAI client, looked up at each
// call because the client starts after the chats. A lookup that returns nil
// leaves the tools without inference.
func (s *Service) WithClient(c func() Client) *Service {
	s.client = c
	return s
}

// WithVisionChooser makes describe_image choose its model with v, which knows
// the chat's own model: a chat model that can look at images can describe
// one itself. Without it, describe_image uses the Resolver's vision model.
func (s *Service) WithVisionChooser(v VisionChooser) *Service {
	s.vision = v
	return s
}

// VisionModel names the model that describes images in a chat whose own model
// is chatModel, or "" when there is none.
func (s *Service) VisionModel(chatModel string) string {
	if s == nil {
		return ""
	}
	m, _ := s.visionChoice(chatModel)
	return m
}

func (s *Service) visionChoice(chatModel string) (string, bool) {
	if s.vision != nil {
		return s.vision(chatModel)
	}
	return s.resolve(Vision), false
}

// call runs fn with the client as the chat's user, reading files from fs.
func (s *Service) call(ctx context.Context, principal string, fs localaitools.FileStore, fn func(ctx context.Context, c Client) error) error {
	var c Client
	if s.client != nil {
		c = s.client()
	}
	if c == nil {
		return errors.New("inference is not available on this server")
	}
	if s.users != nil && principal != "" {
		u, err := s.users(ctx, principal)
		if err != nil {
			return fmt.Errorf("load the chat's user: %w", err)
		}
		ctx = localaitools.WithUser(ctx, u)
	}
	return fn(localaitools.WithFiles(ctx, fs), c)
}

func noModel(c Capability) error {
	switch c {
	case Vision:
		// The model passes this on to the person, who can act on the first half;
		// the second half tells an administrator which setting to use.
		return errors.New("No vision model: choose a model that can look at images for this chat, " +
			"or an administrator can install one from the model gallery and choose it in Settings under Chat image model.")
	}
	return errors.New("No model that can transcribe audio is installed. Install one from the model gallery, or choose one in the settings under Chat transcription model.")
}

// reader is the file store of one call: it serves the file the tool already
// found in the chat's place, or reads it there when the client asks. A
// failure to read keeps its own text, which the client would only wrap.
type reader struct {
	read func(ctx context.Context, ref string, max int64) (localaitools.File, error)
	err  error
	name string // the file that was read, for the messages
}

func (r *reader) Read(ctx context.Context, ref string, max int64) (localaitools.File, error) {
	f, err := r.read(ctx, ref, max)
	if err != nil {
		r.err = err
		return f, err
	}
	r.name = f.Name
	return f, nil
}

func (*reader) Write(context.Context, string, []byte, string) (string, error) {
	return "", errors.New("the media tools do not write files")
}

// fail turns an error of the client into the text the chats always showed. The
// error of reading the file, when there was one, wins.
func fail(err error, r *reader, model string, guess bool) error {
	if r.err != nil {
		return r.err
	}
	var kind *localaitools.WrongKindError
	if errors.As(err, &kind) {
		return fmt.Errorf("%s is not %s", r.name, kind.Kind)
	}
	var se *localaitools.StatusError
	if !errors.As(err, &se) {
		return err
	}
	switch se.Status {
	case http.StatusForbidden:
		return fmt.Errorf("you are not allowed to use the model %s", model)
	case http.StatusTooManyRequests:
		return errors.New("the model refused the request: your quota is used up or the server is busy")
	}
	if guess {
		detail := session.Sanitize(se.Detail)
		if se.Status >= 400 && se.Status < 500 || se.Status >= 500 && imagesUnsupportedRe.MatchString(detail) {
			return notAccepted(model, detail)
		}
	}
	return fmt.Errorf("the model %s answered with an error (%d)", model, se.Status)
}

var imagesUnsupportedRe = regexp.MustCompile(`(?i)image|vision|multimodal|modalit|content.?type|unsupported`)

// notAccepted is what describe_image tells the user when a model that may or
// may not take images refused one: what happened and what to do about it.
func notAccepted(model, detail string) error {
	msg := fmt.Sprintf("The model %s did not accept the image. If it can look at images, turn on \"This model can look at images\" in its settings; "+
		"otherwise choose another model or ask an administrator to set a Chat image model.", model)
	if detail != "" {
		msg += " The upstream said: " + detail
	}
	return errors.New(msg)
}

// describe asks the vision model about the image the reader serves.
func (s *Service) describe(ctx context.Context, principal, model string, guess bool, r *reader, ref, question string) (string, error) {
	var text string
	err := s.call(ctx, principal, r, func(ctx context.Context, c Client) error {
		res, err := c.DescribeImage(ctx, localaitools.DescribeImageRequest{Image: ref, Question: question, Model: model})
		if err != nil {
			return err
		}
		text = res.Description
		return nil
	})
	if err != nil {
		return "", fail(err, r, model, guess)
	}
	return text, nil
}

// transcribe turns the recording the reader serves into text.
func (s *Service) transcribe(ctx context.Context, principal, model string, r *reader, ref string) (string, error) {
	var text string
	err := s.call(ctx, principal, r, func(ctx context.Context, c Client) error {
		res, err := c.TranscribeAudio(ctx, localaitools.TranscribeAudioRequest{Audio: ref, Model: model})
		if err != nil {
			return err
		}
		text = res.Text
		return nil
	})
	if err != nil {
		return "", fail(err, r, model, false)
	}
	return text, nil
}

// held serves one file that is already in memory.
func held(name string, data []byte) *reader {
	return &reader{name: path.Base(name), read: func(_ context.Context, _ string, max int64) (localaitools.File, error) {
		if int64(len(data)) > max {
			return localaitools.File{}, localaitools.ErrFileTooLarge
		}
		return localaitools.File{Name: path.Base(name), Data: data}, nil
	}}
}

// Transcribe turns an audio file into text with the transcription model.
func (s *Service) Transcribe(ctx context.Context, principal, name string, audio io.Reader) (string, error) {
	model := s.resolve(Transcription)
	if model == "" {
		return "", noModel(Transcription)
	}
	data, err := io.ReadAll(audio)
	if err != nil {
		return "", err
	}
	return s.transcribe(ctx, principal, model, held(name, data), name)
}

// IsAudio tells whether a file is a recording by its name, as PrepareUploads
// and transcribe_audio decide.
func IsAudio(name string) bool { return localaitools.IsAudioName(name) }

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
		if !localaitools.IsAudioName(u.Name) || u.Body == nil {
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

// Tools returns the media tools of one chat, whose own model is chatModel.
// They read files of the user's workspace through f, with the paths of the
// chat's place and as its actor, and are not given to sub-agents.
func (s *Service) Tools(f *files.Service, x session.Access, chatModel string) []session.Tool {
	if !f.Available() {
		return nil
	}
	a, place := x.Actor(), x.Place()
	read := func(ctx context.Context, rel string, max int64) (string, []byte, error) {
		t, err := place.Resolve(rel)
		switch {
		case errors.Is(err, workspace.ErrBadName):
			return "", nil, errors.New("the path has a name that is not allowed")
		case errors.Is(err, workspace.ErrNotFound):
			return "", nil, errors.New("not found")
		case err != nil:
			return "", nil, err
		}
		r, n, err := f.Store().Open(ctx, a, t.Abs)
		if err != nil {
			return "", nil, errors.New("not found")
		}
		defer func() { _ = r.Close() }()
		if n.Size > max {
			return "", nil, fmt.Errorf("%s is %d bytes, which is too large", n.Name, n.Size)
		}
		b, err := io.ReadAll(r)
		return n.Name, b, err
	}
	// scoped is the file store of the two aliases: the client reads the file
	// from the chat's place, so an agent's folder fences it like the file tools.
	scoped := func() *reader {
		return &reader{read: func(ctx context.Context, rel string, max int64) (localaitools.File, error) {
			name, b, err := read(ctx, rel, max)
			return localaitools.File{Name: name, Data: b}, err
		}}
	}
	pathProp := `"path":{"type":"string","description":"A path in the user's files: /workspace/home/... is their home folder (/ means the same)."}`
	return []session.Tool{
		{Name: "describe_image", Description: "Describe an image in the user's files, or answer a question about it. Use it for pictures and screenshots, including to read text in them.",
			Schema: json.RawMessage(`{"type":"object","properties":{` + pathProp + `,"question":{"type":"string"}},"required":["path"]}`),
			Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
				in, err := session.DecodeArgs[struct{ Path, Question string }](raw)
				if err != nil {
					return "", err
				}
				model, guess := s.visionChoice(chatModel)
				if model == "" {
					return "", noModel(Vision)
				}
				return s.describe(ctx, a.Principal, model, guess, scoped(), in.Path, in.Question)
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
				if errors.Is(err, extract.ErrNoText) {
					return "", errors.New(documentProblem(name, err))
				}
				if err != nil {
					return "", err
				}
				if doc == "" {
					return "", errors.New(documentProblem(name, nil))
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
				return s.transcribe(ctx, a.Principal, s.resolve(Transcription), scoped(), in.Path)
			}},
	}
}
