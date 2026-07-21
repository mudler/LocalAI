// SPDX-License-Identifier: MIT

package testresources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const ManifestVersion = 1

type Manifest struct {
	Version int        `json:"version"`
	Target  string     `json:"target"`
	HTTP    []HTTP     `json:"http,omitempty"`
	Files   []File     `json:"files,omitempty"`
	Images  []OCIImage `json:"images,omitempty"`
}

type HTTP struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type File struct {
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Destination string `json:"destination,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type OCIImage struct {
	Reference string `json:"reference"`
	SHA256    string `json:"sha256"`
}

type Lock struct {
	Version int               `json:"version"`
	Bundles map[string]string `json:"bundles"`
}

func LoadManifest(path string) (Manifest, error) {
	var manifest Manifest
	if err := decode(path, &manifest); err != nil {
		return manifest, err
	}
	if err := manifest.Validate(); err != nil {
		return manifest, fmt.Errorf("%s: %w", path, err)
	}
	return manifest, nil
}

func LoadLock(path string) (Lock, error) {
	var lock Lock
	if err := decode(path, &lock); err != nil {
		return lock, err
	}
	if lock.Version != ManifestVersion {
		return lock, fmt.Errorf("%s: unsupported version %d", path, lock.Version)
	}
	return lock, nil
}

func decode(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("manifest has trailing JSON data")
	}
	return nil
}

func (m Manifest) Validate() error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("unsupported version %d", m.Version)
	}
	if strings.TrimSpace(m.Target) == "" {
		return errors.New("target is required")
	}
	for _, resource := range m.HTTP {
		if resource.Method == "" || resource.URL == "" || !validDigest(resource.SHA256) {
			return fmt.Errorf("HTTP resources require method, URL, and lowercase sha256: %s %s", resource.Method, resource.URL)
		}
	}
	for _, resource := range m.Files {
		if resource.URL == "" || !validDigest(resource.SHA256) || (resource.Destination == "" && resource.Environment == "") {
			return fmt.Errorf("file resources require URL, sha256, and destination or environment: %s", resource.URL)
		}
		if filepath.IsAbs(resource.Destination) || strings.HasPrefix(filepath.Clean(resource.Destination), "..") {
			return fmt.Errorf("file destination must stay inside the resource directory: %s", resource.Destination)
		}
	}
	for _, resource := range m.Images {
		if !strings.Contains(resource.Reference, "@sha256:") || !validDigest(resource.SHA256) {
			return fmt.Errorf("OCI image must be digest-pinned and have a packed sha256: %s", resource.Reference)
		}
	}
	return nil
}

func BlobPath(cacheDir, digest string) string {
	return filepath.Join(cacheDir, "blobs", "sha256", digest)
}

func VerifyBlob(cacheDir, digest string) (string, error) {
	path := BlobPath(cacheDir, digest)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("missing CAS blob %s: %w", digest, err)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if actual != digest {
		return "", fmt.Errorf("corrupt CAS blob %s: got sha256:%s", digest, actual)
	}
	return path, nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
