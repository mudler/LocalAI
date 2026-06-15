package schema

// This file and type represent a generic request to LocalAI - as opposed to requests to LocalAI-specific endpoints, which live in localai.go
type LocalAIRequest interface {
	ModelName(*string) string
}

// @Description BasicModelRequest contains the basic model request fields
type BasicModelRequest struct {
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
}

func (bmr *BasicModelRequest) ModelName(s *string) string {
	if s != nil {
		bmr.Model = *s
	}
	return bmr.Model
}
