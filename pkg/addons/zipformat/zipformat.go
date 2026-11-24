// SPDX-License-Identifier: MIT

// Package zipformat defines the zip file that carries one add-on package for
// one platform: the files of the OCI package plus a digest manifest. The build
// tool writes it and the owner's installer reads it, so both share these
// names and rules.
package zipformat

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"time"
)

// ManifestName is the digest manifest at the root of the zip. It is not a
// file of the package and the installer does not extract it.
const ManifestName = "zip-manifest.json"

// Schema is the version of the digest manifest.
const Schema = 1

// SourceID, Verification and OriginLabel are what the receipt of a package
// installed from a local file records. Nothing signs such a package.
const (
	SourceID     = "local-file"
	Verification = "sha256"
	OriginLabel  = "local file, unsigned"
)

const (
	zipModeFile    fs.FileMode = 0o644
	zipModeExecute fs.FileMode = 0o755
)

// File is one file of the package.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest lists the identity of the package and every file in it.
type Manifest struct {
	Schema     int    `json:"schema"`
	ID         string `json:"id"`
	Version    string `json:"version"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Executable string `json:"executable"`
	Files      []File `json:"files"`
}

// Entry is a file to write.
type Entry struct {
	Name string
	Data []byte
	Exec bool
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FileName is the name of the zip of a package: <id>-<version>-<os>-<arch>.zip.
func FileName(id, version, os, arch string) string {
	return fmt.Sprintf("%s-%s-%s-%s.zip", id, version, os, arch)
}

// ParseManifest decodes the digest manifest strictly.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return Manifest{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return Manifest{}, errors.New("trailing data after the digest manifest")
	}
	if m.Schema != Schema || m.ID == "" || m.Version == "" || m.OS == "" || m.Arch == "" || m.Executable == "" || len(m.Files) == 0 {
		return Manifest{}, errors.New("incomplete digest manifest")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if f.Path == "" || !shaRE.MatchString(f.SHA256) || f.Size < 0 || seen[f.Path] {
			return Manifest{}, errors.New("invalid file entry in the digest manifest")
		}
		seen[f.Path] = true
	}
	return m, nil
}

// Write writes the zip: the digest manifest first, then the files in sorted
// order with fixed metadata, so the same package gives the same bytes.
func Write(w io.Writer, id, version, os, arch, executable string, files []Entry) error {
	sorted := append([]Entry(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	m := Manifest{Schema: Schema, ID: id, Version: version, OS: os, Arch: arch, Executable: executable}
	for _, f := range sorted {
		sum := sha256.Sum256(f.Data)
		m.Files = append(m.Files, File{Path: f.Name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(f.Data))})
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	put := func(name string, data []byte, mode fs.FileMode) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		h.SetMode(mode)
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	}
	if err := put(ManifestName, append(raw, '\n'), zipModeFile); err != nil {
		return err
	}
	for _, f := range sorted {
		mode := zipModeFile
		if f.Exec {
			mode = zipModeExecute
		}
		if err := put(f.Name, f.Data, mode); err != nil {
			return err
		}
	}
	return zw.Close()
}
