package modelsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/pkg/oci"
)

// OllamaName is the provider id of the Ollama registry.
const OllamaName = "ollama"

// ollamaRegistry serves the models as standard OCI manifests and blobs.
const ollamaRegistry = "https://registry.ollama.ai"

// Layer media types of an Ollama model manifest. Only the model and the
// projector are weights; the others are prompt template, parameters, licence
// text and system prompt.
const (
	ollamaModelLayer     = "application/vnd.ollama.image.model"
	ollamaProjectorLayer = "application/vnd.ollama.image.projector"
)

const manifestAccept = "application/vnd.docker.distribution.manifest.v2+json"

// ollamaSegment is one path segment of a model name or a tag.
var ollamaSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// parseOllamaName reads "name[:tag]" or "user/name[:tag]" and returns the
// canonical "namespace/name:tag": a bare name is in the "library" namespace
// and the default tag is "latest".
func parseOllamaName(s string) (string, error) {
	ns, name, tag := "library", s, "latest"
	if n, t, ok := strings.Cut(name, ":"); ok {
		name, tag = n, t
	}
	if strings.Contains(tag, ":") {
		return "", ErrBadRepo
	}
	if u, n, ok := strings.Cut(name, "/"); ok {
		ns, name = u, n
	}
	for _, seg := range []string{ns, name, tag} {
		if !ollamaSegment.MatchString(seg) || strings.Contains(seg, "..") {
			return "", ErrBadRepo
		}
	}
	return ns + "/" + name + ":" + tag, nil
}

// splitOllamaID splits a canonical id into namespace, name and tag.
func splitOllamaID(id string) (ns, name, tag string) {
	nsName, tag, _ := strings.Cut(id, ":")
	ns, name, _ = strings.Cut(nsName, "/")
	return ns, name, tag
}

func (i *Inspector) fetchOllama(ctx context.Context, ref RepoRef) (*RepoFiles, error) {
	ns, name, tag := splitOllamaID(ref.ID)
	base := i.opt.OllamaBase + "/v2/" + url.PathEscape(ns) + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/manifests/"+url.PathEscape(tag), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", manifestAccept)
	resp, err := i.opt.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w (Ollama registry status 429)", ErrRateLimited)
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s", ErrRepoNotFound, ref.ID)
	default:
		return nil, fmt.Errorf("Ollama registry answered status %d for %s", resp.StatusCode, ref.ID)
	}
	body, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	var m oci.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse Ollama manifest: %w", err)
	}

	short := ns + "/" + name
	if ns == "library" {
		short = name
	}
	out := &RepoFiles{
		Provider: OllamaName,
		ID:       ref.ID,
		URI:      "ollama://" + short + ":" + tag,
		URL:      "https://ollama.com/" + ns + "/" + name,
		Revision: resp.Header.Get("Docker-Content-Digest"),
		Files:    []RepoFile{},
		Variants: []Variant{},
		fileURLs: map[string]string{},
	}
	stem := name + "-" + tag
	for _, l := range m.Layers {
		var f RepoFile
		switch l.MediaType {
		case ollamaModelLayer:
			f = RepoFile{Path: stem + ".gguf", Kind: "gguf"}
		case ollamaProjectorLayer:
			f = RepoFile{Path: stem + "-mmproj.gguf", Kind: "mmproj"}
		default:
			continue
		}
		f.Size = int64(l.Size)
		f.SHA256 = strings.TrimPrefix(l.Digest, "sha256:")
		out.Files = append(out.Files, f)
		if l.Digest != "" {
			out.fileURLs[f.Path] = base + "/blobs/" + url.PathEscape(l.Digest)
		}
		if f.Kind == "gguf" && len(out.Variants) == 0 {
			// One tag is one build of the model, so it is the only variant.
			out.Variants = append(out.Variants, Variant{
				Quantization: tag, File: f.Path, Files: []string{f.Path},
				SizeBytes: f.Size, Shards: 1, Recommended: true,
			})
		}
	}
	for _, f := range out.Files {
		if f.Size > 0 {
			out.SizeKnown = true
			break
		}
	}
	return out, nil
}
