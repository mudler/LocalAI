// SPDX-License-Identifier: MIT

// Package gmailapi is the part of the Gmail v1 REST API that the
// Gmail add-on uses. No address comes from a model, so the base address
// is fixed in the code. A package build or a test may replace it at link time
// with -ldflags "-X <package>.DefaultBase=<url>"; the host never can.
//
// The access token is passed to each call by the caller and is never stored.
// It leaves this package only in the Authorization header, and an error built
// here never holds it, the text of an answer or the address or the text of a mail.
package gmailapi

import (
	"github.com/mudler/LocalAI/addons/internal/googleapi"
)

// DefaultBase is the address of the API. It is a variable only so that the
// link step of a test build can replace it.
var DefaultBase = "https://gmail.googleapis.com"

// MaxBody bounds one answer read in memory. A larger answer is an error, not a
// cut document.
const MaxBody = googleapi.MaxBody

// Errors of a call, shared by the Google add-ons. None of them holds the
// token, the text of the answer or the address or the text of a mail.
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

type (
	// ServerError is a 5xx answer. It is retryable; for a change it also means
	// the outcome is not known.
	ServerError = googleapi.ServerError
	// Response is the part of an answer a caller needs besides the body.
	Response = googleapi.Response
	// Request describes one call. Path starts with a slash, for example
	// /gmail/v1/users/me/messages.
	Request = googleapi.Request
)

// Client calls the API. The zero value is not ready; use New.
type Client struct{ *googleapi.Client }

// New returns a client of the real API.
func New() *Client {
	return &Client{googleapi.New(DefaultBase, "LocalAI-gmail-addon")}
}

var texts = googleapi.Texts{
	ErrUnauthorized: "gmail refused the access token",
	ErrForbidden:    "gmail refused the request",
	ErrNotFound:     "not found, or the connection cannot see it",
	ErrInvalid:      "gmail refused the request",
	ErrUnavailable:  "gmail is not reachable now",
	ErrTooLarge:     "the answer of gmail is too large",
}

// Describe is a fixed sentence for an error of this package, for a refusal. It
// exists so no caller formats the error of a transport.
func Describe(err error) string { return googleapi.Describe(err, texts) }
