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
