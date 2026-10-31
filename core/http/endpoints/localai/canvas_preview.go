// SPDX-License-Identifier: MIT

package localai

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

// canvasPreviewCSP is the Content-Security-Policy of the canvas preview
// route.
//
// Canvas previews run HTML written by a model, and that HTML often loads
// libraries from a public CDN. The global policy (see
// middleware.SecurityHeaders) only allows scripts from 'self', so it blocks
// them. A frame loaded from srcdoc or a blob: URL inherits the policy of the
// parent document, and a <meta> CSP can only tighten it, so the global policy
// cannot be relaxed from inside such a frame. The preview is therefore served
// from its own URL, where the response carries its own header. The header is
// set in the handler, before the global middleware runs its "set if empty"
// check, so the global policy stays unchanged for the rest of the app.
//
// The sandbox directive also applies when someone opens a capability URL as
// a top-level page. It gives model-written HTML an opaque origin, so it cannot
// read the app's cookies or storage or call the API with the user's session.
// The UI's iframe carries the same restriction. Scripts remain available for
// interactive previews; frame-ancestors keeps other sites from embedding the
// route.
const canvasPreviewCSP = "default-src 'none'; " +
	"sandbox allow-scripts; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' blob: " +
	"https://unpkg.com https://cdn.jsdelivr.net https://cdnjs.cloudflare.com " +
	"https://cdn.tailwindcss.com https://code.jquery.com; " +
	"style-src 'self' 'unsafe-inline' https://unpkg.com https://cdn.jsdelivr.net " +
	"https://cdnjs.cloudflare.com https://fonts.googleapis.com; " +
	"img-src data: blob: https:; " +
	"media-src data: blob: https:; " +
	"font-src data: https:; " +
	"connect-src https: blob: data:; " +
	"worker-src blob:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'self'"

const (
	canvasPreviewTTL        = 10 * time.Minute
	canvasPreviewMaxEntries = 128
	canvasPreviewMaxBytes   = 4 << 20
)

type canvasPreviewEntry struct {
	html    []byte
	expires time.Time
}

// CanvasPreviewStore keeps canvas preview HTML in memory for a short time.
// Nothing is written to disk. The id is a random token; the content is not
// logged.
type CanvasPreviewStore struct {
	mu      sync.Mutex
	entries map[string]canvasPreviewEntry
	now     func() time.Time
}

func NewCanvasPreviewStore() *CanvasPreviewStore {
	return &CanvasPreviewStore{entries: map[string]canvasPreviewEntry{}, now: time.Now}
}

// pruneLocked drops expired entries and, if the store is still full, the
// entry that expires first.
func (s *CanvasPreviewStore) pruneLocked() {
	now := s.now()
	for id, e := range s.entries {
		if now.After(e.expires) {
			delete(s.entries, id)
		}
	}
	for len(s.entries) >= canvasPreviewMaxEntries {
		var oldest string
		var oldestExp time.Time
		for id, e := range s.entries {
			if oldest == "" || e.expires.Before(oldestExp) {
				oldest, oldestExp = id, e.expires
			}
		}
		delete(s.entries, oldest)
	}
}

func (s *CanvasPreviewStore) put(html []byte) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.entries[id] = canvasPreviewEntry{html: html, expires: s.now().Add(canvasPreviewTTL)}
	return id, nil
}

func (s *CanvasPreviewStore) get(id string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || s.now().After(e.expires) {
		delete(s.entries, id)
		return nil, false
	}
	return e.html, true
}

// CreateCanvasPreviewEndpoint stores the HTML of a canvas preview and returns
// a short-lived id. POST /api/canvas/preview with {"code": "<html>"}.
func CreateCanvasPreviewEndpoint(s *CanvasPreviewStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, canvasPreviewMaxBytes+1024)
		var req struct {
			Code string `json:"code"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
		}
		if len(req.Code) > canvasPreviewMaxBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "preview too large"})
		}
		id, err := s.put([]byte(req.Code))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not create preview"})
		}
		return c.JSON(http.StatusOK, map[string]string{"id": id})
	}
}

// GetCanvasPreviewEndpoint serves a stored preview with its own CSP.
// GET /api/canvas/preview/:id. The id is an unguessable capability, and the
// route is public because a frame navigation does not carry API key headers.
func GetCanvasPreviewEndpoint(s *CanvasPreviewStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		html, ok := s.get(c.Param("id"))
		if !ok {
			return c.NoContent(http.StatusNotFound)
		}
		h := c.Response().Header()
		h.Set("Content-Security-Policy", canvasPreviewCSP)
		h.Set("Cache-Control", "no-store")
		return c.Blob(http.StatusOK, "text/html; charset=utf-8", html)
	}
}
