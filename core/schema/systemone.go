package schema

import "encoding/json"

// SystemOneRequest is the body for POST /v1/systemone,
// /v1/systemone/separate, and the inner `request` of /v1/systemone/permute.
// Mirrors the kev project's SystemOneRequest (jaredpalmer/kev serve.py).
type SystemOneRequest struct {
	// State is the text (or any JSON value) to extract from. A non-string
	// value is rendered to its JSON representation before NER.
	State json.RawMessage `json:"state"`
	// Questions maps question IDs to their definitions.
	Questions map[string]SystemOneQuestion `json:"questions"`
	// Model names the NER model to use. Optional.
	Model string `json:"model,omitempty"`
	// Threshold is the minimum entity confidence (0–1). Default 0.5.
	Threshold *float32 `json:"threshold,omitempty"`
	// MaxWidth is the maximum span width in tokens. Default 12.
	MaxWidth *int `json:"max_width,omitempty"`
}

// SystemOneQuestion defines one question. Type is "noul", "choice", or
// "score". Instr is optional human-readable instruction text. Criteria
// is:
//   - noul: omitted
//   - choice: a map of option_name → description (each key is a NER label)
//   - score: an array of level descriptions (each is a NER label)
type SystemOneQuestion struct {
	Type     string          `json:"type"`
	Instr    string          `json:"instr,omitempty"`
	Criteria json.RawMessage `json:"criteria,omitempty"`
}

// SystemOneResponse is the shared response shape for /v1/systemone and
// /v1/systemone/separate.
type SystemOneResponse struct {
	Model     string                     `json:"model"`
	Answers   map[string]SystemOneAnswer `json:"answers"`
	Usage     SystemOneUsage             `json:"usage"`
	LatencyMs float64                    `json:"latency_ms"`
}

type SystemOneUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SystemOneAnswer is one question's answer. The fields populated depend on
// the question type:
//   - noul: Noul (float 0–1), Entities
//   - choice: Choice (string), Confidence, Probabilities (map)
//   - score: Score (float), Legend (map), Probabilities (map), Confidence
type SystemOneAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Entities      []SystemOneEntity  `json:"entities,omitempty"`
	Choice        *string            `json:"choice,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

type SystemOneEntity struct {
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float32 `json:"confidence"`
}

// SystemOnePermuteRequest is the body for POST /v1/systemone/permute.
type SystemOnePermuteRequest struct {
	Request  SystemOneRequest `json:"request"`
	Question string           `json:"question"`
	NPerm    int              `json:"n_perm,omitempty"`
	Seed     int64            `json:"seed,omitempty"`
}

// SystemOnePermuteRun is one permutation's result.
type SystemOnePermuteRun struct {
	Order         []string           `json:"order"`
	Probabilities map[string]float64 `json:"probabilities"`
	Choice        string             `json:"choice"`
	LatencyMs     float64            `json:"latency_ms"`
}

// SystemOnePermuteResponse is the response for POST /v1/systemone/permute.
type SystemOnePermuteResponse struct {
	Runs         []SystemOnePermuteRun `json:"runs"`
	ArgmaxStable bool                  `json:"argmax_stable"`
	Spread       map[string]float64    `json:"spread"`
}
