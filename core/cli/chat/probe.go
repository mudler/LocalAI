package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	openai "github.com/sashabaranov/go-openai"
)

var (
	// ErrUnreachable means nothing answered at the endpoint. Callers use this
	// to decide whether offering to start a server makes sense.
	ErrUnreachable = errors.New("no LocalAI server reachable")
	// ErrUnauthorized means the server answered but rejected the credentials.
	ErrUnauthorized = errors.New("LocalAI server rejected the API key")
)

// Probe lists the models the endpoint advertises. It classifies the two
// failures that need different advice: nothing listening, and bad credentials.
//
// The returned list is what the server advertises, verbatim and in server
// order. LocalAI happily lists non-model entries it finds in the models
// directory (stray archives, dotfiles), and guessing which advertised IDs are
// real belongs to whoever presents them, not here.
func Probe(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL

	resp, err := openai.NewClientWithConfig(cfg).ListModels(ctx)
	if err != nil {
		if status, answered := responseStatus(err); answered {
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				return nil, fmt.Errorf("%w: %w", ErrUnauthorized, err)
			}
			// The server answered, so it is up; surface its error as-is.
			return nil, fmt.Errorf("listing models at %s: %w", baseURL, err)
		}
		// A caller who cancelled the probe learned nothing about the endpoint,
		// so claiming it is unreachable would send them to fix a server that
		// may be fine. A deadline is left alone: an endpoint that cannot answer
		// within the probe's budget is unreachable for our purposes.
		var urlErr *url.Error
		if errors.As(err, &urlErr) && !errors.Is(err, context.Canceled) {
			// Only a failure to complete the round trip means nothing is
			// listening. A reply we could not parse is a different problem,
			// so it falls through to the generic error below.
			return nil, fmt.Errorf("%w at %s: %w", ErrUnreachable, baseURL, err)
		}
		return nil, fmt.Errorf("listing models at %s: %w", baseURL, err)
	}

	models := make([]string, 0, len(resp.Models))
	for _, m := range resp.Models {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}

// responseStatus reports the HTTP status a failed call came back with, and
// whether there was one at all.
//
// go-openai splits this across two types depending on the error body, and both
// occur against a real LocalAI: it returns *openai.APIError when the body
// parses as an OpenAI error envelope, which is what LocalAI's normal error
// handler sends, and *openai.RequestError when it does not, which is what
// LocalAI sends when started with opaque errors, since that handler replies
// with a bare status and no body.
func responseStatus(err error) (int, bool) {
	// *RequestError is checked first because it is the outer type when
	// go-openai nests one error inside the other; the inner value in that case
	// carries no status.
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return reqErr.HTTPStatusCode, true
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode, true
	}
	return 0, false
}
