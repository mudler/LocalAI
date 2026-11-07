// SPDX-License-Identifier: MIT

// Package driveapi is the part of the Google Drive v3 REST API that the
// Google Drive add-on uses. No address comes from a model, so the base address
// is fixed in the code. A package build or a test may replace it at link time
// with -ldflags "-X <package>.DefaultBase=<url>"; the host never can.
//
// The access token is passed to each call by the caller and is never stored.
// It leaves this package only in the Authorization header, and an error built
// here never holds it, the text of an answer or the name of a file.
package driveapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/mudler/LocalAI/addons/internal/googleapi"
)

// DefaultBase is the address of the API. It is a variable only so that the
// link step of a test build can replace it.
var DefaultBase = "https://www.googleapis.com"

// MaxBody bounds one answer read in memory. A larger answer is an error, not a
// cut document.
const MaxBody = googleapi.MaxBody

// maxErrorBody bounds the answer read from a stream that failed.
const maxErrorBody = 64 << 10

// Errors of a call, shared by the Google add-ons. None of them holds the
// token, the text of the answer or the name of a file.
var (
	ErrUnauthorized = googleapi.ErrUnauthorized
	ErrForbidden    = googleapi.ErrForbidden
	ErrScope        = googleapi.ErrScope
	ErrNotFound     = googleapi.ErrNotFound
	ErrInvalid      = googleapi.ErrInvalid
	ErrUnavailable  = googleapi.ErrUnavailable
	ErrUnreadable   = googleapi.ErrUnreadable
	ErrTooLarge     = googleapi.ErrTooLarge
	ErrDecode       = googleapi.ErrDecode
)

// ErrRange means the byte range starts after the end of the file (416).
var ErrRange = errors.New("the offset is past the end of the file")

type (
	// ServerError is a 5xx answer. It is retryable; for a change it also means
	// the outcome is not known.
	ServerError = googleapi.ServerError
	// Response is the part of an answer a caller needs besides the body.
	Response = googleapi.Response
	// Request describes one call. Path starts with a slash, for example
	// /drive/v3/files.
	Request = googleapi.Request
)

// Client calls the API. The zero value is not ready; use New.
type Client struct{ *googleapi.Client }

// New returns a client of the real API.
func New() *Client {
	c := googleapi.New(DefaultBase, "LocalAI-gdrive-addon")
	c.StatusErrors = map[int]error{http.StatusRequestedRangeNotSatisfiable: ErrRange}
	return &Client{c}
}

// Open sends the request and returns the body of a 2xx answer as a stream.
// The caller closes it.
func (c *Client) Open(ctx context.Context, token string, r Request) (io.ReadCloser, error) {
	req, err := c.NewRequest(ctx, token, r)
	if err != nil {
		return nil, err
	}
	resp, err := c.Stream.Do(req)
	if err != nil {
		return nil, c.Transport(ctx)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.Body, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return nil, c.Failure(resp.StatusCode, resp.Header, body)
}

// SendRaw is SendJSON for a body that is not JSON, such as the content of a
// file or a multipart upload.
func (c *Client) SendRaw(ctx context.Context, method, token, path string, query url.Values, contentType string, body []byte, v any) error {
	if body == nil {
		body = []byte{}
	}
	return c.Send(ctx, token, Request{Method: method, Path: path, Query: query, Body: body, ContentType: contentType}, v)
}

var texts = googleapi.Texts{
	ErrUnauthorized: "google drive refused the access token",
	ErrForbidden:    "google drive refused the request for this file",
	ErrNotFound:     "not found or the connection cannot see it",
	ErrInvalid:      "google drive refused the request",
	ErrRange:        ErrRange.Error(),
	ErrUnavailable:  "google drive is not reachable now",
	ErrTooLarge:     "the answer of google drive is too large",
}

// Describe is a fixed sentence for an error of this package, for a refusal. It
// exists so no caller formats the error of a transport.
func Describe(err error) string { return googleapi.Describe(err, texts) }
