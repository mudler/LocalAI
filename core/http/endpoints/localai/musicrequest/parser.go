// SPDX-License-Identifier: MIT

// Package musicrequest parses bounded music HTTP inputs. It does not authorize,
// decode audio, establish the handler-entry deadline, or execute inference.
package musicrequest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/mudler/LocalAI/core/services/music"
)

const (
	JSONLimit      = 512 << 10
	MultipartLimit = 26 << 20
	AudioLimit     = 25 << 20
)

type Params struct {
	COT      string
	Seed     int64
	Guidance float64
	Steps    int
}
type Generation struct {
	Model, Style, Lyrics string
	ABC                  *string
	Params               Params
}
type Transcription struct {
	Model string
	Audio *OwnedUpload
}
type Cover struct {
	TranscriptionModel string
	Generation         Generation
	Audio              *OwnedUpload
}

// OwnedUpload is allocated only by trusted storage. Close must join all writers:
// after it returns no asynchronous write may mutate the upload. Close must be
// idempotent (the failure path may close it again). Cleanup must be
// idempotent and acknowledge definitive release through music.CleanupTask.
// Ref is opaque to the parser and must never be derived from a client filename.
type OwnedUpload struct {
	Ref     string
	Writer  io.WriteCloser
	Cleanup func() music.CleanupTask
}

// Create owns all allocations until it returns, including partial failures.
// Whenever any allocation survives Create, it must return an OwnedUpload with
// a Cleanup callback even alongside an error. No allocation may outlive its ACK.
// Implementations provide private, expiry-aware storage; this package supplies none.
type UploadStore interface {
	Create(context.Context) (*OwnedUpload, error)
}
type Parser struct {
	validateABC func(string) error
	uploads     UploadStore
}

// New requires the actual accepted music.ValidateABC at the integration boundary;
// there is deliberately no fallback validator. A nil store permits JSON-only use.
func New(validateABC func(string) error, uploads UploadStore) (*Parser, error) {
	if validateABC == nil {
		return nil, errors.New("music ABC validator required")
	}
	return &Parser{validateABC: validateABC, uploads: uploads}, nil
}

type Error struct {
	Code    int    `json:"code"`
	Type    string `json:"type"`
	Stage   string `json:"stage"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }
func failure(code int, reason string) *Error {
	return &Error{Code: code, Type: "music_stage_error", Stage: "validation", Reason: reason, Message: "Music request validation failed."}
}
func readFailure(err error) error {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		return failure(413, "size_limit")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure(504, "deadline_exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return err
	} // disconnect is not a fabricated HTTP status
	return failure(400, "malformed_request")
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func boundedBody(r *http.Request, limit int64) (io.ReadCloser, error) {
	if r.ContentLength > limit {
		return nil, failure(413, "size_limit")
	}
	if r.Body == nil {
		return nil, failure(400, "missing_body")
	}
	return http.MaxBytesReader(nil, io.NopCloser(contextReader{r.Context(), r.Body}), limit), nil
}

func (p *Parser) ParseGeneration(r *http.Request) (Generation, error) {
	var zero Generation
	if err := mediaType(r, "application/json"); err != nil {
		return zero, err
	}
	body, err := boundedBody(r, JSONLimit)
	if err != nil {
		return zero, err
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return zero, readFailure(err)
	}
	fields, err := object(data)
	if err != nil {
		return zero, err
	}
	if err := r.Context().Err(); err != nil {
		return zero, readFailure(err)
	}
	return p.generation(fields, false)
}
func (p *Parser) ParseTranscription(r *http.Request) (Transcription, error) {
	fields, audio, err := p.multipart(r, false)
	if err != nil {
		return Transcription{}, err
	}
	return Transcription{Model: stringValue(fields["model"]), Audio: audio}, nil
}
func (p *Parser) ParseCover(r *http.Request) (Cover, error) {
	fields, audio, err := p.multipart(r, true)
	if err != nil {
		return Cover{}, err
	}
	// multipart validated generation fields before transferring upload ownership.
	name := stringValue(fields["transcription_model"])
	delete(fields, "transcription_model")
	g, err := p.generation(fields, true)
	if err != nil {
		release(audio)
		return Cover{}, err
	}
	return Cover{TranscriptionModel: name, Generation: g, Audio: audio}, nil
}
func release(u *OwnedUpload) {
	if u == nil {
		return
	}
	if u.Writer != nil {
		_ = u.Writer.Close()
	}
	if u.Cleanup != nil {
		_ = u.Cleanup().Wait()
	}
}
