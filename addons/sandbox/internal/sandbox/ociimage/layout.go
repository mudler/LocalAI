package ociimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
)

// LayoutImage opens the image with digest h in the OCI layout folder dir. It
// checks the manifest, the config and every layer blob against their digests
// before any byte is used, so a folder with swapped blobs fails here and
// nothing is cached under the wrong key.
func LayoutImage(dir string, h v1.Hash) (v1.Image, error) {
	if h.Algorithm != "sha256" {
		return nil, fmt.Errorf("ociimage: layout %s: only sha256 digests are supported", h)
	}
	raw, err := readBlobFile(dir, h)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != h.Hex {
		return nil, fmt.Errorf("ociimage: layout %s: digest mismatch: the manifest hashes to sha256:%x", h, sum)
	}
	m, err := v1.ParseManifest(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ociimage: layout %s: %w", h, err)
	}
	if err := checkBlob(dir, m.Config.Digest); err != nil {
		return nil, err
	}
	for _, l := range m.Layers {
		if err := checkBlob(dir, l.Digest); err != nil {
			return nil, err
		}
	}
	p, err := layout.FromPath(dir)
	if err != nil {
		return nil, err
	}
	return p.Image(h)
}

// maxManifestBytes bounds the manifest that LayoutImage reads into memory.
const maxManifestBytes = 4 << 20

// BlobPath is the file of blob h in the layout folder dir.
func BlobPath(dir string, h v1.Hash) string {
	return filepath.Join(dir, "blobs", h.Algorithm, h.Hex)
}

func readBlobFile(dir string, h v1.Hash) ([]byte, error) {
	f, err := os.Open(BlobPath(dir, h))
	if err != nil {
		return nil, fmt.Errorf("ociimage: layout blob %s: %w", h, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxManifestBytes {
		return nil, fmt.Errorf("ociimage: layout manifest %s is larger than %d bytes", h, maxManifestBytes)
	}
	return raw, nil
}

// checkBlob hashes the blob file of d and fails when the content does not
// have digest d.
func checkBlob(dir string, d v1.Hash) error {
	if d.Algorithm != "sha256" {
		return fmt.Errorf("ociimage: layout blob %s: only sha256 digests are supported", d)
	}
	f, err := os.Open(BlobPath(dir, d))
	if err != nil {
		return fmt.Errorf("ociimage: layout blob %s: %w", d, err)
	}
	defer func() { _ = f.Close() }()
	got, _, err := v1.SHA256(f)
	if err != nil {
		return err
	}
	if got != d {
		return fmt.Errorf("ociimage: layout blob %s: digest mismatch: the content hashes to %s", d, got)
	}
	return nil
}
