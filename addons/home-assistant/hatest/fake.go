// SPDX-License-Identifier: MIT

// Package hatest is a fake of the part of the Home Assistant REST API that the
// Home Assistant add-on uses. It checks the bearer token of each call, keeps
// the calls so a test can see what was asked and with which token, and answers
// the few fixed templates of the add-on by reading the quoted names in them. It
// does not run Jinja: what a real instance answers to those templates is the
// thing a test here cannot prove.
package hatest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is an entity of the fake.
type State struct {
	EntityID   string
	State      string
	Attributes map[string]any
}

// Area is an area of the fake.
type Area struct {
	ID, Name string
	Entities []string
	Devices  int
}

// Service is a service of a domain.
type Service struct {
	Description string
	Fields      map[string]string // field -> description
}

// Point is a recorded state change.
type Point struct {
	At    time.Time
	State string
}

// Entry is a logbook entry.
type Entry struct {
	When          time.Time
	Name, Message string
	EntityID      string
}

// Call is one request the fake served.
type Call struct {
	Method, Path, Query, Token, Body string
}

// Server is a fake instance.
type Server struct {
	*httptest.Server

	mu        sync.Mutex
	token     string
	states    []*State
	areas     []Area
	labels    map[string][]string // label name (lower case) -> entities
	services  map[string]map[string]Service
	history   map[string][]Point
	logbook   map[string][]Entry
	calls     []Call
	version   string
	noLabels  bool   // the templates with labels fail with 400, as an old instance does
	failPath  string // every call on this path prefix answers failStatus
	failCode  int
	dropWrite bool
	redirect  string
}

// New returns a fake that accepts one token.
func New(token string) *Server {
	s := &Server{token: token, labels: map[string][]string{}, services: map[string]map[string]Service{}, history: map[string][]Point{}, logbook: map[string][]Entry{}, version: "2026.11.0"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// NewTLS is New over TLS with a self-signed certificate.
func NewTLS(token string) *Server {
	s := New(token)
	s.Server.Close()
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	return s
}

// Add adds an entity.
func (s *Server) Add(id, state string, attrs map[string]any) *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &State{EntityID: id, State: state, Attributes: attrs}
	s.states = append(s.states, st)
	return st
}

// AddArea adds an area.
func (s *Server) AddArea(a Area) { s.mu.Lock(); s.areas = append(s.areas, a); s.mu.Unlock() }

// Label marks entities with a label.
func (s *Server) Label(name string, ids ...string) {
	s.mu.Lock()
	s.labels[strings.ToLower(name)] = append(s.labels[strings.ToLower(name)], ids...)
	s.mu.Unlock()
}

// NoLabels makes the templates that read labels fail with 400.
func (s *Server) NoLabels() { s.mu.Lock(); s.noLabels = true; s.mu.Unlock() }

// AddService registers a service.
func (s *Server) AddService(domain, name string, sv Service) {
	s.mu.Lock()
	if s.services[domain] == nil {
		s.services[domain] = map[string]Service{}
	}
	s.services[domain][name] = sv
	s.mu.Unlock()
}

// SetHistory sets the recorded states of an entity.
func (s *Server) SetHistory(id string, pts ...Point) { s.mu.Lock(); s.history[id] = pts; s.mu.Unlock() }

// SetLogbook sets the logbook entries of an entity.
func (s *Server) SetLogbook(id string, es ...Entry) { s.mu.Lock(); s.logbook[id] = es; s.mu.Unlock() }

// Fail makes every call whose path starts with prefix answer status.
func (s *Server) Fail(prefix string, status int) {
	s.mu.Lock()
	s.failPath, s.failCode = prefix, status
	s.mu.Unlock()
}

// DropWrites makes the fake apply a service call and cut the connection before it answers.
func (s *Server) DropWrites() { s.mu.Lock(); s.dropWrite = true; s.mu.Unlock() }

// RedirectTo makes every call answer 302 to url.
func (s *Server) RedirectTo(url string) { s.mu.Lock(); s.redirect = url; s.mu.Unlock() }

// StateOf returns the state of an entity, or "".
func (s *Server) StateOf(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.states {
		if st.EntityID == id {
			return st.State
		}
	}
	return ""
}

// Calls returns the requests served.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Posts counts the calls that are not GET and not a template.
func (s *Server) Posts() int {
	n := 0
	for _, c := range s.Calls() {
		if c.Method == http.MethodPost && !strings.HasPrefix(c.Path, "/api/template") {
			n++
		}
	}
	return n
}

// ServiceCalls returns the bodies posted to /api/services/<domain>/<service>.
func (s *Server) ServiceCalls() []Call {
	var out []Call
	for _, c := range s.Calls() {
		if strings.HasPrefix(c.Path, "/api/services/") && c.Method == http.MethodPost {
			out = append(out, c)
		}
	}
	return out
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Token: tok, Body: string(body)})
	redirect, failPath, failCode, drop := s.redirect, s.failPath, s.failCode, s.dropWrite
	s.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	if tok != s.token {
		write(w, http.StatusUnauthorized, map[string]string{"message": "401: Unauthorized"})
		return
	}
	if failPath != "" && strings.HasPrefix(r.URL.Path, failPath) {
		write(w, failCode, map[string]string{"message": "failing"})
		return
	}
	p := r.URL.Path
	switch {
	case p == "/api/config" && r.Method == http.MethodGet:
		write(w, 200, map[string]any{
			"version": s.version, "location_name": "Home", "time_zone": "Europe/Rome", "unit_system": map[string]string{"temperature": "°C", "length": "km"},
			"latitude": 45.1, "longitude": 9.1, "elevation": 120, "currency": "EUR", "country": "IT", "language": "en", "state": "RUNNING",
			"components": []string{"light", "switch", "climate"}, "config_dir": "/config", "external_url": "https://secret.example", "internal_url": "http://10.0.0.2:8123",
			"allowlist_external_dirs": []string{"/config/www"}, "safe_mode": false,
		})
	case p == "/api/states" && r.Method == http.MethodGet:
		s.mu.Lock()
		out := make([]map[string]any, 0, len(s.states))
		for _, st := range s.states {
			out = append(out, st.json())
		}
		s.mu.Unlock()
		write(w, 200, out)
	case strings.HasPrefix(p, "/api/states/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(p, "/api/states/")
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, st := range s.states {
			if st.EntityID == id {
				write(w, 200, st.json())
				return
			}
		}
		write(w, 404, map[string]string{"message": "Entity not found."})
	case p == "/api/services" && r.Method == http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		var domains []string
		for d := range s.services {
			domains = append(domains, d)
		}
		sort.Strings(domains)
		out := []map[string]any{}
		for _, d := range domains {
			svcs := map[string]any{}
			for n, sv := range s.services[d] {
				fields := map[string]any{}
				for f, desc := range sv.Fields {
					fields[f] = map[string]any{"description": desc}
				}
				svcs[n] = map[string]any{"name": n, "description": sv.Description, "fields": fields}
			}
			out = append(out, map[string]any{"domain": d, "services": svcs})
		}
		write(w, 200, out)
	case strings.HasPrefix(p, "/api/services/") && r.Method == http.MethodPost:
		s.service(w, strings.TrimPrefix(p, "/api/services/"), body, drop, r)
	case strings.HasPrefix(p, "/api/history/period/") && r.Method == http.MethodGet:
		id := r.URL.Query().Get("filter_entity_id")
		s.mu.Lock()
		defer s.mu.Unlock()
		pts := s.history[id]
		if len(pts) == 0 {
			write(w, 200, [][]any{})
			return
		}
		row := make([]map[string]any, 0, len(pts))
		for _, pt := range pts {
			row = append(row, map[string]any{"entity_id": id, "state": pt.State, "last_changed": pt.At.UTC().Format(time.RFC3339)})
		}
		write(w, 200, [][]map[string]any{row})
	case strings.HasPrefix(p, "/api/logbook/") && r.Method == http.MethodGet:
		id := r.URL.Query().Get("entity")
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []map[string]any{}
		for _, e := range s.logbook[id] {
			out = append(out, map[string]any{"when": e.When.UTC().Format(time.RFC3339), "name": e.Name, "message": e.Message, "entity_id": e.EntityID})
		}
		write(w, 200, out)
	case p == "/api/template" && r.Method == http.MethodPost:
		s.template(w, body)
	default:
		write(w, 404, map[string]string{"message": "404: Not Found"})
	}
}

func (st *State) json() map[string]any {
	a := map[string]any{}
	for k, v := range st.Attributes {
		a[k] = v
	}
	return map[string]any{"entity_id": st.EntityID, "state": st.State, "attributes": a, "last_changed": "2026-11-04T10:00:00+00:00", "last_updated": "2026-11-04T10:00:00+00:00"}
}

// service applies turn_on/turn_off/toggle/others to the named entities.
func (s *Server) service(w http.ResponseWriter, path string, body []byte, drop bool, r *http.Request) {
	domain, name, _ := strings.Cut(path, "/")
	var in map[string]any
	_ = json.Unmarshal(body, &in)
	var ids []string
	switch v := in["entity_id"].(type) {
	case string:
		ids = []string{v}
	case []any:
		for _, x := range v {
			if str, ok := x.(string); ok {
				ids = append(ids, str)
			}
		}
	}
	s.mu.Lock()
	changed := []map[string]any{}
	for _, id := range ids {
		for _, st := range s.states {
			if st.EntityID != id {
				continue
			}
			switch name {
			case "turn_on":
				st.State = "on"
			case "turn_off":
				st.State = "off"
			case "toggle":
				if st.State == "on" {
					st.State = "off"
				} else {
					st.State = "on"
				}
			case "set_temperature":
				if t, ok := in["temperature"]; ok {
					st.Attributes["temperature"] = t
				}
			}
			changed = append(changed, st.json())
		}
	}
	s.mu.Unlock()
	_ = domain
	if drop {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
	}
	write(w, 200, changed)
}

var quoted = regexp.MustCompile(`"([^"]*)"`)

// template answers the fixed templates of the add-on by their shape.
func (s *Server) template(w http.ResponseWriter, body []byte) {
	var in struct {
		Template string `json:"template"`
	}
	_ = json.Unmarshal(body, &in)
	t := in.Template
	s.mu.Lock()
	defer s.mu.Unlock()
	text := func(v any) { b, _ := json.Marshal(v); w.WriteHeader(200); _, _ = w.Write(b) }
	names := []string{}
	for _, m := range quoted.FindAllStringSubmatch(t, -1) {
		names = append(names, m[1])
	}
	switch {
	case strings.Contains(t, "label_id("):
		if s.noLabels {
			write(w, 400, map[string]string{"message": "TemplateError: UndefinedError: 'label_id' is undefined"})
			return
		}
		out := []string{}
		for _, n := range names {
			out = append(out, s.labels[strings.ToLower(n)]...)
		}
		text(out)
	case strings.Contains(t, "area_name(e)"):
		out := [][]any{}
		for _, id := range names {
			var area any
			for _, a := range s.areas {
				for _, e := range a.Entities {
					if e == id {
						area = a.Name
					}
				}
			}
			out = append(out, []any{id, area})
		}
		text(out)
	case strings.Contains(t, "areas()"):
		out := []map[string]any{}
		for _, a := range s.areas {
			out = append(out, map[string]any{"id": a.ID, "name": a.Name, "entities": len(a.Entities), "devices": a.Devices})
		}
		text(out)
	case strings.Contains(t, "area_entities("):
		out := []string{}
		for _, n := range names {
			for _, a := range s.areas {
				if strings.EqualFold(a.Name, n) || a.ID == n {
					out = append(out, a.Entities...)
				}
			}
		}
		text(out)
	default:
		write(w, 400, map[string]string{"message": "TemplateError: unknown template"})
	}
}

// IntStr is a helper for test data.
func IntStr(n int) string { return strconv.Itoa(n) }
