// SPDX-License-Identifier: MIT
package schema

import "encoding/json"

// DecisionsRequest is the OpenAI Decisions wire contract, distinct from SystemOne.
type DecisionsRequest struct {
	Model string `json:"model"`
	// Input is a text string or an array of user messages. Swagger 2 uses a loose object placeholder for this union.
	Input            json.RawMessage    `json:"input" swaggertype:"object"`
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
	// Value is a string or boolean choice. Swagger 2 uses a loose object placeholder for this union.
	Value       json.RawMessage `json:"value" swaggertype:"object"`
	Description string          `json:"description,omitempty"`
}
type DecisionLevel struct {
	Label       *string `json:"label"`
	Description string  `json:"description,omitempty"`
}
type DecisionInputMessage struct {
	Type string `json:"type,omitempty"`
	Role string `json:"role"`
	// Content is a text string or an array of input_text/input_image parts. Swagger 2 uses a loose object placeholder for this union.
	Content json.RawMessage `json:"content" swaggertype:"object"`
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
	Type        string   `json:"type"`
	Name        *string  `json:"name,omitempty"`
	Probability *float64 `json:"probability,omitempty"`
	// Choice preserves the selected string or boolean value. Swagger 2 uses a loose object placeholder for this union.
	Choice        json.RawMessage       `json:"choice,omitempty" swaggertype:"object"`
	Score         *float64              `json:"score,omitempty"`
	Confidence    *float64              `json:"confidence,omitempty"`
	Probabilities []DecisionProbability `json:"probabilities,omitempty"`
}
type DecisionProbability struct {
	// Value is a string or boolean for choices, or a zero-based integer for score levels. Swagger 2 uses a loose object placeholder for this union.
	Value       json.RawMessage `json:"value" swaggertype:"object"`
	Label       *string         `json:"label,omitempty"`
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
