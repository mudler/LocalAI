// SPDX-License-Identifier: MIT
package cluster

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

// Package describes one release of a fixture add-on.
type Package struct {
	ID      string
	Version string
	// Capabilities is the manifest "capabilities" object.
	Capabilities map[string]any
	// Fixture is written as fixture.json into the package (modes of the
	// protocol fixture). Durable effect logs are enabled with Effects.
	Fixture map[string]any
	// Effects makes the fixture keep its commits in <effects dir>/<id>.log.
	Effects string
	// Files are extra package files, by package path.
	Files map[string]string
}

// Library serves releases as a catalog source on loopback.
type Library struct {
	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
	exe      []byte
	releases []catalog.Release
	archives map[string][]byte
}

// NewLibrary starts a catalog server. exe is the fixture executable that goes
// into every package.
func NewLibrary(exePath string) (*Library, error) {
	exe, err := os.ReadFile(exePath)
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	lib := &Library{listener: l, exe: exe, archives: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/library.json", func(w http.ResponseWriter, r *http.Request) {
		lib.mu.Lock()
		defer lib.mu.Unlock()
		_ = json.NewEncoder(w).Encode(lib.releases)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		lib.mu.Lock()
		data, ok := lib.archives[strings.TrimPrefix(r.URL.Path, "/")]
		lib.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	lib.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = lib.server.Serve(l) }()
	return lib, nil
}

// URL is the library document address.
func (l *Library) URL() string { return "http://" + l.listener.Addr().String() + "/library.json" }

// Close stops the server.
func (l *Library) Close() { _ = l.server.Close() }

// Add builds a package archive and lists it. effectsDir receives the durable
// effect log of packages that ask for one.
func (l *Library) Add(p Package, effectsDir string) error {
	manifest := map[string]any{
		"schemaVersion": 1, "id": p.ID, "version": p.Version, "executable": "bin/fixture",
		"os": runtime.GOOS, "arch": runtime.GOARCH,
		"host": map[string]int{"min": 1, "max": 2}, "api": map[string]int{"min": 1, "max": 1},
		"protocol": map[string]int{"min": 1, "max": 1}, "dataFormat": map[string]int{"min": 1, "max": 2},
		"launchMode": "always", "capabilities": p.Capabilities,
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	add := func(name string, data []byte, mode int64) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := add("manifest.json", raw, 0o644); err != nil {
		return err
	}
	if err := add("bin/fixture", l.exe, 0o755); err != nil {
		return err
	}
	fixture := map[string]any{}
	for k, v := range p.Fixture {
		fixture[k] = v
	}
	if p.Effects != "" {
		fixture["effects"] = filepath.Join(effectsDir, p.ID+".log")
	}
	if len(fixture) > 0 {
		b, _ := json.Marshal(fixture)
		if err := add("fixture.json", b, 0o644); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(p.Files))
	for n := range p.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := add(n, []byte(p.Files[n]), 0o644); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s.tar", p.ID, p.Version)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.archives[name] = buf.Bytes()
	l.releases = append(l.releases, catalog.Release{
		AddonID: p.ID, Name: p.ID, Version: p.Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Digest:   fmt.Sprintf("sha256:%x", sha256.Sum256(buf.Bytes())),
		Artifact: "http://" + l.listener.Addr().String() + "/" + name,
	})
	return nil
}
