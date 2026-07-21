// SPDX-License-Identifier: MIT

package testresources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

var hopHeaders = map[string]bool{
	"Connection": true, "Proxy-Connection": true, "Keep-Alive": true,
	"Transfer-Encoding": true, "Content-Length": true, "Te": true,
	"Trailer": true, "Upgrade": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true,
}

func LoadHTTPIndex(cacheDir string) (map[string]HTTPEntry, error) {
	index := map[string]HTTPEntry{}
	data, err := os.ReadFile(filepath.Join(cacheDir, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read HTTP cache index: %w", err)
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse HTTP cache index: %w", err)
	}
	return index, nil
}

func WriteHTTPIndex(cacheDir string, index map[string]HTTPEntry) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cacheDir, "index-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(cacheDir, "index.json"))
}

func SanitizeHeaders(header http.Header) http.Header {
	out := header.Clone()
	for name := range hopHeaders {
		out.Del(name)
	}
	return out
}

func ReplayResponse(w http.ResponseWriter, cacheDir string, entry HTTPEntry) error {
	path, err := VerifyBlob(cacheDir, entry.Digest)
	if err != nil {
		return err
	}
	for name, values := range entry.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.Header().Set("Content-Length", fmt.Sprint(entry.Size))
	w.WriteHeader(entry.Status)
	if entry.Size == 0 {
		return nil
	}
	body, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	_, err = io.Copy(w, body)
	return err
}
