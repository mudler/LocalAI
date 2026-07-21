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

// FetchRepoFiles asks the models API for one repo. A 404 yields (nil, nil) so
// that probing for an optional counterpart repo is not an error.
func FetchRepoFiles(client *http.Client, repo string) ([]GGUFFile, error) {
	url := fmt.Sprintf("https://huggingface.co/api/models/%s?blobs=true", repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "localai-apexentries/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %d", repo, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return ParseRepoFiles(body)
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
