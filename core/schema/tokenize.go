package schema

type TokenizeRequest struct {
	Content string `json:"content"`
	Model   string `json:"model"`
}

type TokenizeResponse struct {
	Tokens []int32 `json:"tokens"`
}
