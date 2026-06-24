package schema

// RerankRequest defines the structure of the request payload
type JINARerankRequest struct {
	BasicModelRequest
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      *int     `json:"top_n,omitempty"`
	Backend   string   `json:"backend"`
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
