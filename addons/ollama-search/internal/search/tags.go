// SPDX-License-Identifier: MIT
package search

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Variant is one tag of a library model: a build of it that can be pulled.
type Variant struct {
	// Tag is the part after the colon, for example 8b or latest.
	Tag string `json:"tag"`
	// URI is what the importer takes: ollama://<name>:<tag>.
	URI    string `json:"uri"`
	Digest string `json:"digest,omitempty"`
	// SizeBytes is the download size the page shows, in decimal units.
	SizeBytes int64 `json:"size_bytes,omitempty"`
	// ContextTokens is the context window the page shows. The page writes 40K
	// for 40960 tokens, so a K is 1024.
	ContextTokens int64 `json:"context_tokens,omitempty"`
}

// nonGGUFMarkers are parts of a tag name that mark a build that is not a GGUF
// file: MLX and the nvfp4 and mxfp8 formats keep their weights as safetensors
// tensors, which llama.cpp cannot load. Tags such as bf16, fp16 or q4_K_M are
// GGUF builds and are kept.
var nonGGUFMarkers = []string{"mlx", "nvfp4", "mxfp8"}

func isGGUFTag(tag string) bool {
	t := strings.ToLower(tag)
	for _, m := range nonGGUFMarkers {
		if strings.Contains(t, m) {
			return false
		}
	}
	return true
}

// maxVariants is the most tags the add-on returns for one model.
const maxVariants = 200

// The detail line of a tag: <digest> • <size> • <context> context window.
var (
	tagDetail  = regexp.MustCompile(`([0-9a-f]{12})\s*•\s*([0-9]+(?:\.[0-9]+)?)\s*([KMGT]B)(?:\s*•\s*([0-9]+(?:\.[0-9]+)?)\s*([KM])\s+context window)?`)
	sizeUnits  = map[string]float64{"KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}
	ctxFactors = map[string]float64{"K": 1 << 10, "M": 1 << 20}
)

// ParseTags reads the tags page of the model name. The page lists each tag
// twice, once for narrow screens and once for wide ones; the add-on reads the
// first, which carries the digest, size and context window in one block. A tag
// of a cloud-hosted build has no download, and a tag of a build that is not
// a GGUF file (MLX, nvfp4, mxfp8) cannot run with llama.cpp; both are left out. A page with no tag
// is an error.
func ParseTags(body, name string) ([]Variant, error) {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("the Ollama tags page of %s could not be read: %w", name, ErrLayout)
	}
	prefix := "/library/" + name + ":"
	links := findAll(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && strings.HasPrefix(attr(n, "href"), prefix) &&
			strings.Contains(" "+attr(n, "class")+" ", " md:hidden ")
	})
	out := []Variant{}
	for _, a := range links {
		tag := strings.TrimPrefix(attr(a, "href"), prefix)
		if tag == "" || len(tag) > 100 || strings.ContainsAny(tag, "/?# ") {
			return nil, fmt.Errorf("the Ollama tags page of %s lists a tag with an unexpected link (the site markup may have changed): %w", name, ErrLayout)
		}
		if !isGGUFTag(tag) {
			continue
		}
		m := tagDetail.FindStringSubmatch(text(a))
		if m == nil {
			if strings.HasSuffix(tag, "cloud") {
				continue
			}
			return nil, fmt.Errorf("the Ollama tags page of %s has a tag with no size line (the site markup may have changed): %w", name, ErrLayout)
		}
		v := Variant{Tag: tag, URI: "ollama://" + name + ":" + tag, Digest: m[1]}
		size, _ := strconv.ParseFloat(m[2], 64)
		v.SizeBytes = int64(math.Round(size * sizeUnits[m[3]]))
		if m[4] != "" {
			c, _ := strconv.ParseFloat(m[4], 64)
			v.ContextTokens = int64(math.Round(c * ctxFactors[m[5]]))
		}
		out = append(out, v)
		if len(out) >= maxVariants {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the Ollama tags page of %s lists no tags (the site markup may have changed): %w", name, ErrLayout)
	}
	return out, nil
}
