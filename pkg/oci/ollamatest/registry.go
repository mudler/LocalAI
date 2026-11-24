// Package ollamatest serves a fake Ollama registry over HTTP for tests, so no
// test needs the network.
package ollamatest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/oci"
)

// Layer is one layer of a fake model.
type Layer struct {
	MediaType string
	// Data is the content the manifest declares (size and digest).
	Data []byte
	// Served, when set, is what the registry sends instead of Data, to fake
	// a corrupted blob.
	Served []byte
	// Truncate makes the registry declare the full length but stop sending
	// halfway.
	Truncate bool
}

// Model is a fake model.
type Model struct {
	Layers []Layer
	// ManifestStatus replaces the 200 of the manifest response.
	ManifestStatus int
	// ManifestBody replaces the generated manifest body.
	ManifestBody []byte
}

// Registry is a fake Ollama registry.
type Registry struct {
	server *httptest.Server

	mu     sync.Mutex
	models map[string]Model
	blobs  map[string]Layer
	// Requests counts the requests the registry received, by path.
	Requests []string
}

// New starts a registry. Close it when the test ends.
func New() *Registry {
	r := &Registry{models: map[string]Model{}, blobs: map[string]Layer{}}
	r.server = httptest.NewServer(http.HandlerFunc(r.serve))
	return r
}

// Close stops the registry.
func (r *Registry) Close() { r.server.Close() }

// Host is the "host:port" of the registry.
func (r *Registry) Host() string { return strings.TrimPrefix(r.server.URL, "http://") }

// Use points the Ollama pulls of this process at the registry. The returned
// function undoes it.
func (r *Registry) Use() (restore func()) { return oci.UseOllamaRegistry(r.Host(), true) }

// Digest is the sha256 digest of data in OCI notation.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Add registers a model under "<repository>/<name>:<tag>", for example
// "library/qwen3:8b".
func (r *Registry) Add(ref string, m Model) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[ref] = m
	for _, l := range m.Layers {
		r.blobs[Digest(l.Data)] = l
	}
}

// Paths returns the request paths received so far.
func (r *Registry) Paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.Requests...)
}

func (r *Registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.Requests = append(r.Requests, req.URL.Path)
	r.mu.Unlock()

	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/v2/"), "/")
	if len(parts) < 4 {
		http.NotFound(w, req)
		return
	}
	kind, last := parts[len(parts)-2], parts[len(parts)-1]
	repo := strings.Join(parts[:len(parts)-2], "/")

	switch kind {
	case "manifests":
		r.mu.Lock()
		m, ok := r.models[repo+":"+last]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		if m.ManifestStatus != 0 {
			w.WriteHeader(m.ManifestStatus)
			return
		}
		body := m.ManifestBody
		if body == nil {
			type layer struct {
				MediaType string `json:"mediaType"`
				Digest    string `json:"digest"`
				Size      int    `json:"size"`
			}
			doc := struct {
				SchemaVersion int     `json:"schemaVersion"`
				MediaType     string  `json:"mediaType"`
				Layers        []layer `json:"layers"`
			}{SchemaVersion: 2, MediaType: "application/vnd.docker.distribution.manifest.v2+json"}
			for _, l := range m.Layers {
				doc.Layers = append(doc.Layers, layer{l.MediaType, Digest(l.Data), len(l.Data)})
			}
			body, _ = json.Marshal(doc)
		}
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		_, _ = w.Write(body)
	case "blobs":
		r.mu.Lock()
		l, ok := r.blobs[last]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		data := l.Data
		if l.Served != nil {
			data = l.Served
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Docker-Content-Digest", last)
		if req.Method == http.MethodHead {
			return
		}
		if l.Truncate {
			_, _ = w.Write(data[:len(data)/2])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, req)
	}
}
