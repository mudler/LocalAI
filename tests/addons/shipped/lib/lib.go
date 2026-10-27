// SPDX-License-Identifier: MIT

// Package lib serves a local add-on library for the shipped add-on suite. It
// takes the OCI layouts that scripts/addons/package.sh made, joins the layers
// of each into the plain archive a local source installs, and serves
// library.yaml and the archives on a loopback port.
//
// Every release is also listed once more as <version>-badsum, whose digest
// does not match the archive, so a suite can check that an install refuses a
// package that differs from its catalog entry. Nothing is signed or pushed.
package lib

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
)

// Serve serves the releases named by specs (<id>@<version>=<layout>) and
// returns the URL of the library document and a function that stops it.
func Serve(listen string, specs []string) (string, func(), error) {
	l, err := net.Listen("tcp", listen)
	if err != nil {
		return "", nil, err
	}
	base := "http://" + l.Addr().String()
	archives := map[string][]byte{}
	var releases []catalog.Release
	for _, spec := range specs {
		idVersion, layout, ok := strings.Cut(spec, "=")
		id, version, ok2 := strings.Cut(idVersion, "@")
		if !ok || !ok2 {
			_ = l.Close()
			return "", nil, fmt.Errorf("argument %q is not <id>@<version>=<layout>", spec)
		}
		archive, err := mergeLayers(layout)
		if err != nil {
			_ = l.Close()
			return "", nil, fmt.Errorf("%s: %w", spec, err)
		}
		sum := sha256.Sum256(archive)
		name := fmt.Sprintf("%s-%s.tar", id, version)
		archives[name] = archive
		releases = append(releases, catalog.Release{
			ID: id, Name: id, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH,
			Digest: "sha256:" + hex.EncodeToString(sum[:]), Artifact: base + "/" + name,
		})
		badName := fmt.Sprintf("%s-%s-badsum.tar", id, version)
		archives[badName] = archive
		releases = append(releases, catalog.Release{
			ID: id, Name: id, Version: version + "-badsum", OS: runtime.GOOS, Arch: runtime.GOARCH,
			Digest: "sha256:" + strings.Repeat("0", 64), Artifact: base + "/" + badName,
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/library.yaml", func(w http.ResponseWriter, r *http.Request) {
		_ = catalog.WriteIndex(w, releases)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, ok := archives[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	return base + "/library.yaml", func() { _ = srv.Close() }, nil
}

// mergeLayers turns the layers of an OCI layout into one plain tar, in the
// order the receipt of package.sh lists them, after it checked each layer
// against its digest. A publishable receipt is refused: a suite never serves
// a package meant for release.
func mergeLayers(layout string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(layout, "receipt.json")) // #nosec G304 -- test harness reads files it created in its own work directory
	if err != nil {
		return nil, err
	}
	var receipt struct {
		Publishable bool `json:"publishable"`
		Layers      []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, err
	}
	if receipt.Publishable {
		return nil, fmt.Errorf("a local package must not be publishable")
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	seen := map[string]bool{}
	for _, l := range receipt.Layers {
		hexDigest := strings.TrimPrefix(l.Digest, "sha256:")
		blob, err := os.ReadFile(filepath.Join(layout, "blobs", "sha256", hexDigest)) // #nosec G304 -- test harness reads files it created in its own work directory
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(blob)
		if hex.EncodeToString(sum[:]) != hexDigest {
			return nil, fmt.Errorf("layer %s does not match its digest", l.Digest)
		}
		zr, err := gzip.NewReader(bytes.NewReader(blob))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(zr)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if seen[h.Name] {
				return nil, fmt.Errorf("duplicate path %s in the layers", h.Name)
			}
			seen[h.Name] = true
			if err := tw.WriteHeader(&tar.Header{Name: h.Name, Mode: h.Mode, Size: h.Size, Typeflag: h.Typeflag}); err != nil {
				return nil, err
			}
			if _, err := io.CopyN(tw, tr, h.Size); err != nil { // #nosec G110 -- the layers are the harness's own
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if !seen["manifest.json"] {
		return nil, fmt.Errorf("the package lacks its manifest")
	}
	return out.Bytes(), nil
}
