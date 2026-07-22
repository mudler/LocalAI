// SPDX-License-Identifier: MIT

package testresources

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func PackBundle(cacheDir, output string, manifest Manifest) (string, error) {
	index, err := LoadHTTPIndex(cacheDir)
	if err != nil {
		return "", err
	}
	targetIndex := map[string]HTTPEntry{}
	digests := map[string]bool{}
	for _, resource := range manifest.HTTP {
		key := RequestKey(resource.Method, resource.URL, resource.Headers())
		entry, ok := index[key]
		if !ok {
			return "", fmt.Errorf("cannot pack missing HTTP entry %s", key)
		}
		targetIndex[key], digests[resource.SHA256] = entry, true
	}
	for _, resource := range manifest.Files {
		digests[resource.SHA256] = true
	}
	for _, resource := range manifest.Images {
		digests[resource.SHA256] = true
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), "bundle-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	hash := sha256.New()
	gzipWriter, err := gzip.NewWriterLevel(io.MultiWriter(tmp, hash), gzip.BestSpeed)
	if err != nil {
		_ = tmp.Close()
		return "", err
	}
	gzipWriter.Name = ""
	gzipWriter.Comment = ""
	gzipWriter.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.OS = 255
	tw := tar.NewWriter(gzipWriter)
	indexData, err := json.Marshal(targetIndex)
	if err == nil {
		err = writeTarBytes(tw, "http-index.json", indexData)
	}
	ordered := make([]string, 0, len(digests))
	for digest := range digests {
		ordered = append(ordered, digest)
	}
	sort.Strings(ordered)
	for _, digest := range ordered {
		if err != nil {
			break
		}
		path, verifyErr := VerifyBlob(cacheDir, digest)
		if verifyErr != nil {
			err = verifyErr
			break
		}
		var data []byte
		data, err = os.ReadFile(path)
		if err == nil {
			err = writeTarBytes(tw, filepath.ToSlash(filepath.Join("blobs", "sha256", digest)), data)
		}
	}
	err = errors.Join(err, tw.Close(), gzipWriter.Close(), tmp.Close())
	if err != nil {
		return "", err
	}
	if err := os.Rename(name, output); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func RestoreBundle(cacheDir, bundle, expected string) error {
	data, err := os.ReadFile(bundle)
	if err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != expected {
		return fmt.Errorf("test resource bundle checksum mismatch: expected %s, got %s", expected, actual)
	}
	var bundleReader io.Reader = bytes.NewReader(data)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gzipReader, err := gzip.NewReader(bundleReader)
		if err != nil {
			return err
		}
		defer func() { _ = gzipReader.Close() }()
		bundleReader = gzipReader
	}
	tr := tar.NewReader(bundleReader)
	recorded := map[string]HTTPEntry{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe bundle path %q", header.Name)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if name == "http-index.json" {
			if err := json.Unmarshal(body, &recorded); err != nil {
				return err
			}
			continue
		}
		destination := filepath.Join(cacheDir, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, body, 0o644); err != nil {
			return err
		}
	}
	index, err := LoadHTTPIndex(cacheDir)
	if err != nil {
		return err
	}
	for key, entry := range recorded {
		index[key] = entry
	}
	return WriteHTTPIndex(cacheDir, index)
}

func writeTarBytes(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
