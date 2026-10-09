// SPDX-License-Identifier: MIT
package schema

import "encoding/json"

// DecisionsRequest is the OpenAI Decisions wire contract, distinct from SystemOne.
type DecisionsRequest struct {
	Model            string             `json:"model"`
	Input            json.RawMessage    `json:"input"`
	Questions        []DecisionQuestion `json:"questions"`
	SafetyIdentifier *string            `json:"safety_identifier,omitempty"`
}

type DecisionQuestion struct {
	Type         string           `json:"type"`
	Name         *string          `json:"name,omitempty"`
	Instructions *string          `json:"instructions"`
	Choices      []DecisionChoice `json:"choices,omitempty"`
	Levels       []DecisionLevel  `json:"levels,omitempty"`
}
type DecisionChoice struct {
	Value       json.RawMessage `json:"value"`
	Description string          `json:"description,omitempty"`
}
type DecisionLevel struct {
	Label       *string `json:"label"`
	Description string  `json:"description,omitempty"`
}
type DecisionInputMessage struct {
	Type    string          `json:"type,omitempty"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type DecisionInputPart struct {
	Type     string  `json:"type"`
	Text     *string `json:"text,omitempty"`
	ImageURL string  `json:"image_url,omitempty"`
	Detail   *string `json:"detail,omitempty"`
}
type DecisionsResponse struct {
	Model   string           `json:"model"`
	Answers []DecisionAnswer `json:"answers"`
	Usage   DecisionUsage    `json:"usage"`
}
type DecisionAnswer struct {
	Type          string                `json:"type"`
	Name          *string               `json:"name,omitempty"`
	Probability   *float64              `json:"probability,omitempty"`
	Choice        json.RawMessage       `json:"choice,omitempty"`
	Score         *float64              `json:"score,omitempty"`
	Confidence    *float64              `json:"confidence,omitempty"`
	Probabilities []DecisionProbability `json:"probabilities,omitempty"`
}
type DecisionProbability struct {
	Value       json.RawMessage `json:"value"`
	Label       string          `json:"label,omitempty"`
	Probability float64         `json:"probability"`
}

// Unknown accounting details are omitted, not invented as zero counts.
type DecisionUsage struct {
	InputTokens         int            `json:"input_tokens"`
	OutputTokens        int            `json:"output_tokens"`
	TotalTokens         int            `json:"total_tokens"`
	InputTokensDetails  map[string]int `json:"input_tokens_details"`
	OutputTokensDetails map[string]int `json:"output_tokens_details"`
}
