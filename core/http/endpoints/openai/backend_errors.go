package openai

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// contextOverflowMarkers are fragments of the errors backends return when a
// prompt does not fit the model's context:
//
//   - llama.cpp: "request (9739 tokens) exceeds the available context size (8192 tokens), try increasing it"
//   - llama.cpp, context shift on: "input (9739 tokens) is larger than the max context size (8192 tokens). skipping"
//   - vLLM: "This model's maximum context length is 8192 tokens. However, ..."
var contextOverflowMarkers = []string{
	"exceeds the available context size",
	"is larger than the max context size",
	"maximum context length",
}

// backendRequestError maps a backend error to the HTTP error the client gets.
// A prompt that does not fit the context is the client's request, not a server
// fault, so it is a 400, as in the OpenAI API and llama-server. The message is
// kept whole: clients read the token counts from it to learn the real window.
// Other errors are returned unchanged and become a 500.
func backendRequestError(err error) error {
	msg := err.Error()
	for _, marker := range contextOverflowMarkers {
		if strings.Contains(msg, marker) {
			return echo.NewHTTPError(http.StatusBadRequest, msg)
		}
	}
	return err
}
