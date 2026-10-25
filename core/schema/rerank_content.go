// SPDX-License-Identifier: MIT
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/pkg/embeddingmedia"
)

// RerankContent is the typed form of a rerank request whose query or documents
// carry content objects. A request of one string query and string documents is
// the legacy form and never produces one.
type RerankContent struct {
	Query     EmbeddingInputItem
	Documents []EmbeddingInputItem
}

func rerankMalformed() error {
	return &embeddingmedia.Error{Status: 400, Code: "invalid_rerank_input"}
}

// ParseRerankRequest looks for typed content in a rerank request body. The
// second result is false for the legacy form (a string query and string
// documents), which stays with the existing binder, untouched. Typed input is
// validated with the embedding rules (strict fields, duplicate keys, bounded
// base64 and URL shape) plus the rerank rules: parts are text or image_url,
// and within one query or document the text parts come first, because the
// backend appends images to the text and must not reorder anything silently.
func ParseRerankRequest(raw json.RawMessage) (*RerankContent, bool, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false, nil
	}
	queryName, documentsName := embeddingKeyFold("query"), embeddingKeyFold("documents")
	var query, documents json.RawMessage
	seen := map[string]bool{}
	duplicate := false
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, false, nil
		}
		key, ok := token.(string)
		if !ok {
			return nil, false, nil
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, false, nil
		}
		folded := embeddingKeyFold(key)
		if folded != queryName && folded != documentsName {
			continue
		}
		duplicate = duplicate || seen[folded]
		seen[folded] = true
		if folded == queryName {
			query = value
		} else {
			documents = value
		}
	}
	if _, err = d.Token(); err != nil {
		return nil, false, nil
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false, nil
	}

	var entries []json.RawMessage
	arrayDocuments := json.Unmarshal(documents, &entries) == nil
	query = bytes.TrimSpace(query)
	structured := len(query) > 0 && query[0] == '{'
	for _, e := range entries {
		if len(e) > 0 && e[0] == '{' {
			structured = true
		}
	}
	if !structured {
		return nil, false, nil
	}
	if duplicate || !arrayDocuments || len(entries) == 0 || len(query) == 0 || (query[0] != '"' && query[0] != '{') {
		return nil, true, rerankMalformed()
	}
	if err := embeddingmedia.CheckLimit(uint64(len(raw)), embeddingmedia.MaxBodyBytes); err != nil {
		return nil, true, err
	}

	// Rerank shares the request budget of embeddings. Items are not charged:
	// the number of documents is bounded by the source part limit.
	budget := embeddingmedia.NewBudget()
	content := &RerankContent{Documents: make([]EmbeddingInputItem, 0, len(entries))}
	if content.Query, err = parseRerankItem(query, &budget); err != nil {
		return nil, true, err
	}
	for _, e := range entries {
		item, err := parseRerankItem(bytes.TrimSpace(e), &budget)
		if err != nil {
			return nil, true, err
		}
		content.Documents = append(content.Documents, item)
	}
	if err := checkRerankItem(content.Query); err != nil {
		return nil, true, err
	}
	for _, item := range content.Documents {
		if err := checkRerankItem(item); err != nil {
			return nil, true, err
		}
	}
	return content, true, nil
}

// parseRerankItem parses one query or document. A string and the content shape
// go through the embedding parser. The llama.cpp-style object
// {"text": string|[string], "image": url|[url]} is mapped onto the same item the
// content shape produces: one text part first, then one image_url part per
// image, so budgets, acquisition and the typed RPC see no difference.
func parseRerankItem(entry json.RawMessage, budget *embeddingmedia.Budget) (EmbeddingInputItem, error) {
	var item EmbeddingInputItem
	if len(entry) == 0 || entry[0] != '{' {
		return parseEmbeddingItem(entry, budget)
	}
	obj, err := embeddingObject(entry, "content", "text", "image")
	if err != nil {
		return item, err
	}
	if obj["content"] != nil {
		if obj["text"] != nil || obj["image"] != nil {
			return item, &embeddingmedia.Error{Status: 400, Code: "rerank_mixed_shapes"}
		}
		return parseEmbeddingItem(entry, budget)
	}
	texts, err := rerankStrings(obj["text"])
	if err != nil {
		return item, err
	}
	images, err := rerankStrings(obj["image"])
	if err != nil {
		return item, err
	}
	if len(texts) == 0 && len(images) == 0 {
		return item, rerankMalformed()
	}
	text := strings.Join(texts, "")
	if len(images) == 0 {
		item.Text = &text
		return item, budget.Consume(embeddingmedia.Consumption{TextBytes: uint64(len(text)), SourceParts: 1})
	}
	parts := len(images)
	if text != "" {
		parts++
	}
	if err = budget.Consume(embeddingmedia.Consumption{SourceParts: uint64(parts)}); err != nil {
		return item, err
	}
	if text != "" {
		if err = budget.Consume(embeddingmedia.Consumption{TextBytes: uint64(len(text))}); err != nil {
			return item, err
		}
		item.Content = append(item.Content, EmbeddingContentPart{Type: "text", Text: text})
	}
	for _, img := range images {
		part, err := rerankImagePart(img, budget)
		if err != nil {
			return item, err
		}
		item.Content = append(item.Content, part)
	}
	return item, nil
}

// rerankStrings reads a JSON string or a non-empty array of strings. A missing
// key yields nothing; null, numbers and objects are malformed.
func rerankStrings(raw json.RawMessage) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		s, err := embeddingString(raw)
		if err != nil {
			return nil, err
		}
		return []string{s}, nil
	}
	var entries []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &entries) != nil || len(entries) == 0 {
		return nil, rerankMalformed()
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		s, err := embeddingString(bytes.TrimSpace(e))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// rerankImagePart turns one "image" string into an image_url part. Accepted: an
// http(s) URL, a base64 data URI, or a raw base64 string. A raw string is held
// as inline data, so it meets the same bounded decode and signature sniff as a
// data URI at acquisition and is never read as a path. Anything with another
// scheme, and anything path-like, is refused.
func rerankImagePart(s string, b *embeddingmedia.Budget) (EmbeddingContentPart, error) {
	p := EmbeddingContentPart{Type: "image_url"}
	useDataURI := &embeddingmedia.Error{Status: 400, Code: "rerank_image_use_data_uri"}
	switch {
	case s == "":
		return p, rerankMalformed()
	case strings.HasPrefix(s, "data:"):
		header, data, ok := strings.Cut(s, ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return p, rerankMalformed()
		}
		p.Source.URL = s
		return p, validateEmbeddingBase64(data, b)
	case strings.Contains(s, ":"):
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return p, rerankMalformed()
		}
		p.Source.URL = s
		return p, nil
	case strings.ContainsAny(s[:1], "/.~") || strings.Contains(s, "\\"):
		return p, useDataURI
	}
	if err := validateEmbeddingBase64(s, b); err != nil {
		var classified *embeddingmedia.Error
		if errors.As(err, &classified) && classified.Status == 413 {
			return p, err
		}
		return p, useDataURI
	}
	p.Source.Data = s
	return p, nil
}

// checkRerankItem enforces the part types and order a reranker can honour.
func checkRerankItem(item EmbeddingInputItem) error {
	sawImage := false
	for _, p := range item.Content {
		switch p.Type {
		case "text":
			if sawImage {
				return rerankMalformed()
			}
		case "image_url":
			sawImage = true
		default:
			return &embeddingmedia.Error{Status: 422, Code: "rerank_unsupported_modality"}
		}
	}
	return nil
}

// HasMedia reports whether the request carries any image, so callers that must
// never fall back to text know that dropping a part would change the meaning.
func (c *RerankContent) HasMedia() bool {
	if c == nil {
		return false
	}
	items := append([]EmbeddingInputItem{c.Query}, c.Documents...)
	for _, it := range items {
		for _, p := range it.Content {
			if p.Type != "text" {
				return true
			}
		}
	}
	return false
}

// RerankQueryObject and the types below describe the structured rerank input for
// the API schema. They are documentation only: requests are validated by
// ParseRerankRequest.

// RerankContentObject is one structured query or document. Its parts are taken
// in order: text parts first, then image parts.
type RerankContentObject struct {
	// Content is a non-empty ordered list of text and image_url parts.
	Content []EmbeddingContentPartDoc `json:"content"`
}

// RerankTextImageObject is the llama.cpp-style form of a query or document. Both
// keys are optional but one is required, and it cannot be mixed with content.
type RerankTextImageObject struct {
	// Text is a string or an array of strings, joined in order without separators.
	Text any `json:"text,omitempty"`
	// Image is an http(s) URL, a base64 data URI or a raw base64 string, or an array of them.
	Image any `json:"image,omitempty"`
}
