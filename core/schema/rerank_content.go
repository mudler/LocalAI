// SPDX-License-Identifier: MIT
package schema

import (
	"bytes"
	"encoding/json"
	"io"

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
	if content.Query, err = parseEmbeddingItem(query, &budget); err != nil {
		return nil, true, err
	}
	for _, e := range entries {
		item, err := parseEmbeddingItem(bytes.TrimSpace(e), &budget)
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
