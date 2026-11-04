// SPDX-License-Identifier: MIT

// Package pikvmtest is a fake of the part of the PiKVM (kvmd) HTTP API that
// the PiKVM add-on uses. It checks the X-KVMD-User and X-KVMD-Passwd headers
// of each call, keeps the calls so a test can see what was asked and with
// which credential, applies the ATX and HID commands it receives, and can
// answer with a redirect or an error status, or cut the connection after it
// applied a command.
package pikvmtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Call is one request the fake served.
type Call struct {
	Method string
	Path   string
	Query  string
	User   string
	Passwd string
	Body   string
}

// Server is a fake PiKVM.
type Server struct {
	*httptest.Server

	mu        sync.Mutex
	user      string
	passwd    string
	calls     []Call
	power     bool
	hdd       bool
	atx       bool
	busy      bool
	kbOnline  bool
	hidOn     bool
	typed     []string
	keys      []string
	shortcuts []string
	clicks    []string
	actions   []string
	failPath  string
	failCode  int
	redirect  string
	dropWrite bool
}

// New returns a fake that accepts one user and password, over plain HTTP.
func New(user, passwd string) *Server {
	s := &Server{user: user, passwd: passwd, atx: true, kbOnline: true, hidOn: true, power: true}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// NewTLS is New over TLS with a self-signed certificate.
func NewTLS(user, passwd string) *Server {
	s := &Server{user: user, passwd: passwd, atx: true, kbOnline: true, hidOn: true, power: true}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	return s
}

func (s *Server) with(f func()) { s.mu.Lock(); f(); s.mu.Unlock() }

// SetPower sets the power LED.
func (s *Server) SetPower(on bool) { s.with(func() { s.power = on }) }

// SetATX sets whether ATX control is enabled and whether it is busy.
func (s *Server) SetATX(enabled, busy bool) { s.with(func() { s.atx, s.busy = enabled, busy }) }

// SetKeyboard sets whether the keyboard is online.
func (s *Server) SetKeyboard(online bool) { s.with(func() { s.kbOnline = online }) }

// Fail makes every call on a path prefix answer with the status.
func (s *Server) Fail(prefix string, status int) {
	s.with(func() { s.failPath, s.failCode = prefix, status })
}

// DropWrites makes the fake apply a command and cut the connection before it answers.
func (s *Server) DropWrites() { s.with(func() { s.dropWrite = true }) }

// RedirectTo makes every call answer 302 to url.
func (s *Server) RedirectTo(url string) { s.with(func() { s.redirect = url }) }

// Power reports the power LED.
func (s *Server) Power() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.power }

// Calls returns the requests served.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Typed returns the texts the fake received on the print endpoint.
func (s *Server) Typed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.typed...)
}

// Keys returns the keys of send_key calls.
func (s *Server) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys...)
}

// Shortcuts returns the key lists of send_shortcut calls.
func (s *Server) Shortcuts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.shortcuts...)
}

// Actions returns the actions of /api/atx/power calls, and Clicks the buttons of /api/atx/click calls.
func (s *Server) Actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.actions...)
}
func (s *Server) Clicks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.clicks...)
}

// Writes counts the calls that change something.
func (s *Server) Writes() int {
	n := 0
	for _, c := range s.Calls() {
		if c.Method == http.MethodPost {
			n++
		}
	}
	return n
}

func reply(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	ok := status >= 200 && status < 300
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "result": result})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u, p := r.Header.Get("X-KVMD-User"), r.Header.Get("X-KVMD-Passwd")
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, User: u, Passwd: p, Body: string(body)})
	redirect, failPath, failCode, drop := s.redirect, s.failPath, s.failCode, s.dropWrite
	s.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	if u != s.user || p != s.passwd {
		reply(w, http.StatusForbidden, map[string]string{"error": "ForbiddenError", "error_msg": "Forbidden"})
		return
	}
	if failPath != "" && strings.HasPrefix(r.URL.Path, failPath) {
		reply(w, failCode, map[string]string{"error": "Failing"})
		return
	}
	q := r.URL.Query()
	write := func(apply func()) bool {
		if r.Method != http.MethodPost {
			reply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method"})
			return false
		}
		s.mu.Lock()
		busy, atx := s.busy, s.atx
		s.mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/api/atx") && (busy || !atx) {
			reply(w, http.StatusConflict, map[string]string{"error": "AtxIsBusyError"})
			return false
		}
		s.with(apply)
		if drop {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return false
				}
			}
		}
		return true
	}
	switch r.URL.Path {
	case "/api/info":
		reply(w, 200, map[string]any{
			"hw": map[string]any{
				"health":   map[string]any{"temp": map[string]any{"cpu": 48.5}},
				"platform": map[string]any{"base": "Raspberry Pi 4 Model B", "model": "v3", "type": "rpi", "board": "rpi4", "serial": "SERIAL-NUMBER-0001", "video": "hdmi"},
			},
			"system": map[string]any{"kvmd": map[string]any{"version": "3.300"}, "streamer": map[string]any{"version": "6.1"}, "kernel": map[string]any{"release": "6.6.0-pi"}},
			"meta":   map[string]any{"server": map[string]any{"host": "secret-hostname.example"}},
		})
	case "/api/atx":
		s.mu.Lock()
		res := map[string]any{"enabled": s.atx, "busy": s.busy, "leds": map[string]bool{"power": s.power, "hdd": s.hdd}}
		s.mu.Unlock()
		reply(w, 200, res)
	case "/api/atx/power":
		if write(func() {
			act := q.Get("action")
			s.actions = append(s.actions, act)
			switch act {
			case "on":
				s.power = true
			case "off", "off_hard":
				s.power = false
			}
		}) {
			reply(w, 200, map[string]any{})
		}
	case "/api/atx/click":
		if write(func() {
			b := q.Get("button")
			s.clicks = append(s.clicks, b)
			if b == "power_long" {
				s.power = !s.power
			}
		}) {
			reply(w, 200, map[string]any{})
		}
	case "/api/hid":
		s.mu.Lock()
		res := map[string]any{"enabled": s.hidOn, "online": s.hidOn, "busy": false, "connected": true,
			"keyboard": map[string]any{"online": s.kbOnline, "leds": map[string]bool{"caps": true, "num": false, "scroll": false}}}
		s.mu.Unlock()
		reply(w, 200, res)
	case "/api/hid/print":
		if write(func() { s.typed = append(s.typed, string(body)) }) {
			reply(w, 200, map[string]any{})
		}
	case "/api/hid/events/send_key":
		if write(func() { s.keys = append(s.keys, q.Get("key")) }) {
			reply(w, 200, map[string]any{})
		}
	case "/api/hid/events/send_shortcut":
		if write(func() { s.shortcuts = append(s.shortcuts, q.Get("keys")) }) {
			reply(w, 200, map[string]any{})
		}
	default:
		reply(w, http.StatusNotFound, map[string]string{"error": "NotFound"})
	}
}
