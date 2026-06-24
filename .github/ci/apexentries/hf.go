package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// ErrNoSHA256 marks a GGUF the HuggingFace API describes without an
// lfs.sha256. Emitting an entry without a hash would ship an unverifiable
// download, and guessing one from another field is how a Xet hash ends up
// masquerading as a content hash, so this is fatal rather than skippable.
var ErrNoSHA256 = errors.New("gguf file has no lfs.sha256")

// GGUFFile is one .gguf sibling of a HuggingFace repo.
type GGUFFile struct {
	Name   string
	Size   int64
	SHA256 string
}

type apiSibling struct {
	RFilename string `json:"rfilename"`
	Size      int64  `json:"size"`
	LFS       *struct {
		SHA256 string `json:"sha256"`
	} `json:"lfs"`
}

type apiModel struct {
	Siblings []apiSibling `json:"siblings"`
}

// ParseRepoFiles returns every .gguf sibling described by a models API body.
func ParseRepoFiles(body []byte) ([]GGUFFile, error) {
	var m apiModel
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decoding model response: %w", err)
	}

	var out []GGUFFile
	for _, s := range m.Siblings {
		if !strings.HasSuffix(s.RFilename, ".gguf") {
			continue
		}
		if s.LFS == nil || s.LFS.SHA256 == "" {
			return nil, fmt.Errorf("%s: %w", s.RFilename, ErrNoSHA256)
		}
		out = append(out, GGUFFile{Name: s.RFilename, Size: s.Size, SHA256: s.LFS.SHA256})
	}
	return out, nil
}

// FetchOptionalRepoFiles asks the models API for a repo the caller can do
// without, and reports separately whether the repo was merely unreadable.
//
// HuggingFace answers 401 Unauthorized, not 404, for a repo that does not exist
// when the request carries no credentials. Without a token there is therefore no
// way to tell "this repo was never published" from "this repo is private", so an
// optional probe has to treat 401 and 403 exactly like 404: whatever the reason,
// there is nothing here for us to read, so there is no counterpart.
//
// The second return value exists because that collapse is lossy in one
// direction: 401/403 can also mean a real, gated repo whose quants we would
// genuinely want. The caller reports those repos so a silently dropped
// counterpart is visible to a human rather than invisible.
func FetchOptionalRepoFiles(client *http.Client, repo string) ([]GGUFFile, bool, error) {
	files, status, err := fetchRepoFiles(client, repo)
	if err != nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		return nil, true, nil
	}
	return files, false, err
}

// FetchRepoFiles asks the models API for one repo. A 404 yields (nil, nil) so
// that probing for an optional counterpart repo is not an error. Every other
// non-200, 401 and 403 included, is an error: for a repo the run REQUIRES there
// is no benign reading of "we cannot see it".
func FetchRepoFiles(client *http.Client, repo string) ([]GGUFFile, error) {
	files, _, err := fetchRepoFiles(client, repo)
	return files, err
}

// fetchRepoFiles does the request and returns the HTTP status alongside the
// result, so the optional and required callers can apply different policies to
// the same response without duplicating the request.
func fetchRepoFiles(client *http.Client, repo string) ([]GGUFFile, int, error) {
	url := fmt.Sprintf("https://huggingface.co/api/models/%s?blobs=true", repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "localai-apexentries/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("%s: unexpected status %d", repo, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	files, err := ParseRepoFiles(body)
	return files, resp.StatusCode, err
}

// newHTTPClient builds the client used against the HuggingFace API. It goes
// through pkg/httpclient rather than a bare &http.Client{} because the std
// client follows redirects and forwards custom credential headers to the
// redirect target on a cross-host hop (GHSA-3mj3-57v2-4636). This caller sends
// only a User-Agent today, but it talks to an external API that could start
// redirecting, and an HF_TOKEN header here later would then leak.
func newHTTPClient() *http.Client {
	return httpclient.NewWithTimeout(60 * time.Second)
}
