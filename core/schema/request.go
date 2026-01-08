package schema

// This file and type represent a generic request to LocalAI - as opposed to requests to LocalAI-specific endpoints, which live in localai.go
type LocalAIRequest interface {
	ModelName(*string) string
}

// @Description BasicModelRequest contains the basic model request fields
type BasicModelRequest struct {
	Model string `json:"model,omitempty" yaml:"model,omitempty"`
	// TODO: Should this also include the following fields from the OpenAI side of the world?
	// If so, changes should be made to core/http/middleware/request.go to match

	// Context context.Context    `json:"-"`
	// Cancel  context.CancelFunc `json:"-"`
}

func (bmr *BasicModelRequest) ModelName(s *string) string {
	if s != nil {
		bmr.Model = *s
	}
	return bmr.Model
}
