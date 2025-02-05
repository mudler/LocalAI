package schema

type TokenizeRequest struct {
	BasicModelRequest
	Content string `json:"content"`
}

type TokenizeResponse struct {
	Tokens []int32 `json:"tokens"`
}
