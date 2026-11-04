// SPDX-License-Identifier: MIT

// Command oauthfixture is a test-only add-on for the OAuth conformance suite.
// It is not shipped. The suite packages this one executable several times,
// with manifests that name different services, so a host that treated one
// service in a special way would fail for the others.
//
// It knows nothing about any service. Its one tool, whoami, answers with what
// the host gave the call: a hash of the credential, the granted scopes, the
// request with the credential replaced by its hash, and the environment and
// arguments of the process, and the connector calls the host made with the
// token replaced by its hash. The suite checks that no refresh token, client
// secret, code or verifier is anywhere in that answer. The argument reject
// lists credential hashes the add-on rejects as the service would (401
// credential_rejected); "all" rejects every credential.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"sync"

	"github.com/mudler/LocalAI/addons/internal/idle"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type runtime struct {
	idle.Runtime
	addonID string
	// connector keeps the connector calls the host made, with the token
	// replaced by its hash, so whoami can report them.
	connector *calls
}

type calls struct {
	mu   sync.Mutex
	list []contract.ConnectorRequest
}

// credentialHash is how the fixture names a credential in its answers.
func credentialHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (r runtime) Read(_ context.Context, e contract.Envelope) (contract.Result, error) {
	var args struct {
		Tool   string   `json:"tool"`
		Reject []string `json:"reject"`
	}
	_ = json.Unmarshal(e.Request.Arguments, &args)
	hash := credentialHash(e.Request.Credential)
	if e.Request.Credential == "" || slices.Contains(args.Reject, "all") || slices.Contains(args.Reject, hash) {
		return contract.Result{}, contract.ErrCredentialRejected
	}
	req := e.Request
	req.Credential = "sha256:" + hash
	payload, err := json.Marshal(map[string]any{
		"addon":         r.addonID,
		"credential":    hash,
		"grantedScopes": e.Request.GrantedScopes,
		"request":       req,
		"environment":   os.Environ(), //nolint:forbidigo // the fixture reports the exact environment the host gave it
		"arguments":     os.Args,
		"connector":     r.connector.snapshot(),
	})
	if err != nil {
		return contract.Result{}, err
	}
	return contract.Result{Payload: payload}, nil
}

// Connector answers the op me with an account name made from the add-on ID,
// and refuses a call without a token. Other ops have nothing to deliver.
func (r runtime) Connector(_ context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	r.connector.add(req)
	if req.Op != contract.ConnectorOpMe {
		return contract.ConnectorResponse{}, nil
	}
	if req.Token == "" {
		return contract.ConnectorResponse{Error: &contract.ConnectorError{Code: contract.ConnectorUnauthorized}}, nil
	}
	return contract.ConnectorResponse{BotUsername: "person@" + r.addonID + ".example.test"}, nil
}

func (c *calls) add(req contract.ConnectorRequest) {
	if req.Token != "" {
		req.Token = "sha256:" + credentialHash(req.Token)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.list) < 64 {
		c.list = append(c.list, req)
	}
}

func (c *calls) snapshot() []contract.ConnectorRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.list)
}

func main() {
	os.Exit(protocol.Run(os.Args, func(st protocol.Startup) contract.Runtime {
		return runtime{addonID: st.Registration.AddonID, connector: &calls{}}
	}))
}
