// SPDX-License-Identifier: MIT
package packaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// Roles of the layers of a platform image, in order. The ui layer exists only
// when the manifest declares a UI.
const (
	roleExecutable = "executable"
	roleUI         = "ui"
	roleManifest   = "manifest"
)

// pkgError carries a stable code so a test (and a reader of CI logs) can tell
// which package rule failed.
type pkgError struct{ code, msg string }

func (e *pkgError) Error() string { return e.code + ": " + e.msg }

func fail(code, format string, a ...any) error {
	return &pkgError{code: code, msg: fmt.Sprintf(format, a...)}
}

// Receipt is the digest receipt written next to the layout.
type Receipt struct {
	Schema         int            `json:"schema"`
	Addon          string         `json:"addon"`
	Version        string         `json:"version"`
	Platform       string         `json:"platform"`
	Revision       string         `json:"revision,omitempty"`
	Publishable    bool           `json:"publishable"`
	ManifestDigest string         `json:"manifestDigest"`
	ConfigDigest   string         `json:"configDigest"`
	Layers         []ReceiptLayer `json:"layers"`
}

type ReceiptLayer struct {
	Role      string `json:"role"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
}

const receiptFile = "receipt.json"

// leg is one platform image read from a layout, with every blob re-hashed.
type leg struct {
	dir        string
	descriptor v1.Descriptor
	manifest   v1.Manifest
	rawMan     []byte
	config     v1.ConfigFile
}

func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return errors.New("trailing data")
	}
	return nil
}

// readBlob returns the bytes of a blob after checking digest and size.
func readBlob(dir string, d v1.Hash, size int64) ([]byte, error) {
	if d.Algorithm != "sha256" || len(d.Hex) != 64 {
		return nil, fail("E_DIGEST", "unsupported digest %q", d.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "blobs", d.Algorithm, d.Hex)) // #nosec G304 -- the packaging tool reads paths the operator passes on its command line
	if err != nil {
		return nil, fail("E_DIGEST", "blob %s is missing", d)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != d.Hex {
		return nil, fail("E_DIGEST", "blob %s does not hash to its digest", d)
	}
	if size >= 0 && int64(len(b)) != size {
		return nil, fail("E_DIGEST", "blob %s has size %d, descriptor says %d", d, len(b), size)
	}
	return b, nil
}

func readRootIndex(dir string) (*v1.IndexManifest, error) {
	marker, err := os.ReadFile(filepath.Join(dir, "oci-layout")) // #nosec G304 -- the packaging tool reads paths the operator passes on its command line
	if err != nil {
		return nil, fail("E_LAYOUT", "oci-layout file is missing")
	}
	var m struct {
		Version string `json:"imageLayoutVersion"`
	}
	if err := strictJSON(marker, &m); err != nil || m.Version != "1.0.0" {
		return nil, fail("E_LAYOUT", "unsupported oci-layout marker")
	}
	b, err := os.ReadFile(filepath.Join(dir, "index.json")) // #nosec G304 -- the packaging tool reads paths the operator passes on its command line
	if err != nil {
		return nil, fail("E_LAYOUT", "index.json is missing")
	}
	var idx v1.IndexManifest
	if err := strictJSON(b, &idx); err != nil {
		return nil, fail("E_LAYOUT", "index.json: %v", err)
	}
	if idx.SchemaVersion != 2 {
		return nil, fail("E_LAYOUT", "index.json schemaVersion %d", idx.SchemaVersion)
	}
	return &idx, nil
}

// loadManifestBlob reads one image manifest and its config by digest.
func loadLegByDescriptor(dir string, d v1.Descriptor) (*leg, error) {
	if d.MediaType != types.OCIManifestSchema1 {
		return nil, fail("E_LAYERS", "manifest media type %q is not an OCI image manifest", d.MediaType)
	}
	raw, err := readBlob(dir, d.Digest, d.Size)
	if err != nil {
		return nil, err
	}
	l := &leg{dir: dir, descriptor: d, rawMan: raw}
	if err := strictJSON(raw, &l.manifest); err != nil {
		return nil, fail("E_LAYERS", "image manifest: %v", err)
	}
	if l.manifest.MediaType != types.OCIManifestSchema1 {
		return nil, fail("E_LAYERS", "image manifest mediaType %q", l.manifest.MediaType)
	}
	cfg, err := readBlob(dir, l.manifest.Config.Digest, l.manifest.Config.Size)
	if err != nil {
		return nil, err
	}
	if l.manifest.Config.MediaType != types.OCIConfigJSON {
		return nil, fail("E_LAYERS", "config media type %q", l.manifest.Config.MediaType)
	}
	if err := json.Unmarshal(cfg, &l.config); err != nil {
		return nil, fail("E_LAYERS", "image config: %v", err)
	}
	return l, nil
}

func loadLeg(dir string) (*leg, *Receipt, error) {
	idx, err := readRootIndex(dir)
	if err != nil {
		return nil, nil, err
	}
	if len(idx.Manifests) != 1 {
		return nil, nil, fail("E_LAYOUT", "a platform layout holds exactly one image, found %d", len(idx.Manifests))
	}
	l, err := loadLegByDescriptor(dir, idx.Manifests[0])
	if err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, receiptFile)) // #nosec G304 -- the packaging tool reads paths the operator passes on its command line
	if err != nil {
		return nil, nil, fail("E_DIGEST", "digest receipt is missing")
	}
	var r Receipt
	if err := strictJSON(b, &r); err != nil {
		return nil, nil, fail("E_DIGEST", "digest receipt: %v", err)
	}
	return l, &r, nil
}

// copyBytes is io.Copy into a buffer; kept tiny so call sites read clearly.
func readAll(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("too large")
	}
	return b, nil
}
