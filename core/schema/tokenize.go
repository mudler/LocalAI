package schema

type TokenizeRequest struct {
	BasicModelRequest
	Content string `json:"content"` // text to tokenize
}

type TokenizeResponse struct {
	Tokens []int32 `json:"tokens"` // token IDs
}
