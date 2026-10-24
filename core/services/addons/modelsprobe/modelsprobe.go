// SPDX-License-Identifier: MIT

// Package modelsprobe checks that the add-on owner and a frontend see the same
// models storage. The owner writes a small file with a random nonce into its
// models path; a frontend reads the file from its own models path and compares
// the nonce. It depends on nothing else in the add-on host, so the gateway and
// the application share it.
package modelsprobe

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// FileName starts with a dot and ends in .json so the model loader and the
// config loader skip it.
const FileName = ".localai-addons-probe.json"

// maxSize bounds a read: a probe is a few hundred bytes.
const maxSize = 4096

// ErrNotShared is the typed refusal of an export or import when the probe of
// the owner's own models path is missing or changed.
var ErrNotShared = errors.New("the models path is not the shared models storage; see the add-ons docs")

// Probe is the content of the probe file.
type Probe struct {
	Nonce      string `json:"nonce"`
	ModelsPath string `json:"modelsPath"`
	WrittenAt  string `json:"writtenAt"`
}

// Valid reports whether the probe carries a nonce.
func (p Probe) Valid() bool { return len(p.Nonce) == 64 && p.ModelsPath != "" }

// Same reports whether two probes name the same nonce and the same absolute
// path. A symlink that points at the same storage differs on the path rule.
func Same(a, b Probe) bool {
	return a.Valid() && b.Valid() && a.Nonce == b.Nonce && a.ModelsPath == b.ModelsPath
}

// Write puts a new probe in dir. The file is written to a temporary name,
// synced and renamed, so a reader never sees a partial file.
func Write(dir string) (Probe, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Probe{}, err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return Probe{}, err
	}
	p := Probe{Nonce: hex.EncodeToString(raw), ModelsPath: abs, WrittenAt: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.Marshal(p)
	if err != nil {
		return Probe{}, err
	}
	tmp, err := os.CreateTemp(abs, ".localai-addons-probe-*.tmp")
	if err != nil {
		return Probe{}, err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Probe{}, err
	}
	if err = os.Chmod(name, 0o644); err != nil {
		return Probe{}, err
	}
	if err = os.Rename(name, filepath.Join(abs, FileName)); err != nil {
		return Probe{}, err
	}
	if d, derr := os.Open(abs); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return p, nil
}

// Read returns the probe in dir. A missing, oversized or malformed file is an
// error.
func Read(dir string) (Probe, error) {
	f, err := os.Open(filepath.Join(dir, FileName))
	if err != nil {
		return Probe{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return Probe{}, err
	}
	if len(data) > maxSize {
		return Probe{}, fmt.Errorf("probe file is too large")
	}
	var p Probe
	if err = json.Unmarshal(data, &p); err != nil {
		return Probe{}, err
	}
	if !p.Valid() {
		return Probe{}, fmt.Errorf("probe file is not valid")
	}
	return p, nil
}
