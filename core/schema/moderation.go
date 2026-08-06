package schema

import (
	"encoding/json"
	"fmt"
)

// ModerationInput accepts the text-only forms supported by the OpenAI
// moderations API. Multimodal moderation can be added without changing the
// response contract once LocalAI has a moderation-capable vision path.
type ModerationInput []string

func (i *ModerationInput) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*i = ModerationInput{single}
		return nil
	}

	var multiple []string
	if err := json.Unmarshal(data, &multiple); err == nil {
		*i = ModerationInput(multiple)
		return nil
	}

	return fmt.Errorf("input must be a text string or array of text strings")
}

func (i ModerationInput) MarshalJSON() ([]byte, error) {
	if len(i) == 1 {
		return json.Marshal(i[0])
	}
	return json.Marshal([]string(i))
}

type ModerationRequest struct {
	BasicModelRequest
	Input ModerationInput `json:"input"`
}

type ModerationResult struct {
	Flagged                   bool                `json:"flagged"`
	Categories                map[string]bool     `json:"categories"`
	CategoryScores            map[string]float64  `json:"category_scores"`
	CategoryAppliedInputTypes map[string][]string `json:"category_applied_input_types"`
}

type ModerationResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Results []ModerationResult `json:"results"`
}
