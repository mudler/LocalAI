package schema

import (
	"bytes"
	"encoding/json"
)

// RerankRequest defines the structure of the request payload.
//
// query is a string or a content object, and each document is a string or a
// content object. A content object is {"content":[part,...]} with text and
// image_url parts, exactly as for embeddings. When any of them is a content
// object the request is typed: Query and Documents stay empty and Typed holds
// the parsed form. The HTTP binder fills Typed before the handler runs.
type JINARerankRequest struct {
	BasicModelRequest
	Query     string         `json:"query"`
	Documents []string       `json:"documents"`
	TopN      *int           `json:"top_n,omitempty"`
	Backend   string         `json:"backend"`
	Typed     *RerankContent `json:"-"`
}

// UnmarshalJSON accepts the legacy form exactly as the default decoder did, and
// tolerates content objects for query and documents, which ParseRerankRequest
// validates. Anything else keeps failing with the decoder's own error.
func (r *JINARerankRequest) UnmarshalJSON(b []byte) error {
	type legacy JINARerankRequest
	var plain legacy
	err := json.Unmarshal(b, &plain)
	if err == nil {
		*r = JINARerankRequest(plain)
		return nil
	}
	var shadow struct {
		BasicModelRequest
		Query     json.RawMessage   `json:"query"`
		Documents []json.RawMessage `json:"documents"`
		TopN      *int              `json:"top_n,omitempty"`
		Backend   string            `json:"backend"`
	}
	if json.Unmarshal(b, &shadow) != nil {
		return err
	}
	typed := bytes.HasPrefix(bytes.TrimSpace(shadow.Query), []byte("{"))
	for _, d := range shadow.Documents {
		typed = typed || bytes.HasPrefix(bytes.TrimSpace(d), []byte("{"))
	}
	if !typed {
		return err
	}
	*r = JINARerankRequest{BasicModelRequest: shadow.BasicModelRequest, TopN: shadow.TopN, Backend: shadow.Backend}
	return nil
}

// DocumentResult represents a single document result
type JINADocumentResult struct {
	Index          int      `json:"index"`
	Document       JINAText `json:"document"`
	RelevanceScore float64  `json:"relevance_score"`
}

// Text holds the text of the document
type JINAText struct {
	Text string `json:"text"`
}

// RerankResponse defines the structure of the response payload
type JINARerankResponse struct {
	Model   string               `json:"model"`
	Usage   JINAUsageInfo        `json:"usage"`
	Results []JINADocumentResult `json:"results"`
}

// UsageInfo holds information about usage of tokens
type JINAUsageInfo struct {
	TotalTokens  int `json:"total_tokens"`
	PromptTokens int `json:"prompt_tokens"`
}
