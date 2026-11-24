package schema

type TokenizeRequest struct {
	BasicModelRequest
	Content string `json:"content"` // text to tokenize
}

type TokenizeResponse struct {
	Tokens []int32 `json:"tokens"` // token IDs
}

type DetokenizeRequest struct {
	BasicModelRequest
	Tokens []int32 `json:"tokens"` // token IDs to convert back to text
}

type DetokenizeResponse struct {
	Content string `json:"content"` // detokenized text
}

// SlotCacheRequest names a model slot. Restore also sends the ID that a
// save returned. The client never chooses a file name. Slot defaults to 0.
type SlotCacheRequest struct {
	BasicModelRequest
	ID   string `json:"id,omitempty"` // restore only: an ID issued by a save for this model
	Slot int32  `json:"slot"`         // slot id, 0 is the first slot
}

// SlotCacheResponse reports what was saved or restored.
type SlotCacheResponse struct {
	Model      string  `json:"model"`
	Action     string  `json:"action"` // "save" or "restore"
	Slot       int32   `json:"slot"`
	ID         string  `json:"id"`                // issued by the save, sent back to restore
	Message    string  `json:"message,omitempty"` // for example the IDs a save pruned
	NTokens    uint64  `json:"n_tokens"`          // tokens saved or restored
	NBytes     uint64  `json:"n_bytes"`           // bytes written or read
	DurationMS float64 `json:"duration_ms"`       // time the backend spent
}
