package provider

import (
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"

	"github.com/mudler/LocalAI/core/http/auth"
)

// baseURL is a placeholder: the host never resolves, because the client's
// transport answers every request itself.
const baseURL = "http://studio.invalid/v1"

// Provider hands out model clients that call the application's chat route.
type Provider struct{ transport *Transport }

// New returns a Provider whose transport has no handler yet.
func New() *Provider { return &Provider{transport: NewTransport()} }

// Transport returns the transport, so the HTTP server can set its router.
func (p *Provider) Transport() *Transport { return p.transport }

// LLM returns a model client for model that acts as user. Every request goes
// through the application's chat route, so that route's checks decide whether
// user may use the model. A nil user is for the case where auth is off.
func (p *Provider) LLM(user *auth.User, model string) cogito.LLM {
	llm := clients.NewLocalAILLM(model, "", baseURL)
	llm.SetHTTPClient(p.transport.Client(user))
	return llm
}
