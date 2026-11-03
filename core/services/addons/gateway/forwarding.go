// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var ErrForbidden = errors.New("addon forwarding denied")

// PeerIdentity must be derived by the carrier from a verified TLS chain and
// explicit certificate identity binding, never HTTP forwarding headers.
type PeerIdentity struct {
	ID       string
	Verified bool
}

type ForwardedRequest struct {
	PeerID      string         `json:"peerId"`
	Audience    string         `json:"audience"`
	Actor       contract.Actor `json:"actor"`
	OperationID string         `json:"operationId"`
	Nonce       string         `json:"nonce"`
	ExpiresAt   time.Time      `json:"expiresAt"`
	Body        []byte         `json:"body"`
	// Chat is the chat the frontend made the call for, when a chat made it.
	// It is covered by the signature, and the owner records it next to the
	// envelope (hostops.InFlight) so the add-on's workspace operations act
	// with the chat's access. It is never put in the envelope the add-on
	// receives. Nil (omitted) for a call that no chat made, so such a
	// request signs as before.
	Chat *hostops.Binding `json:"chat,omitempty"`
	// HumanSurface says the receiving frontend found the request to come from
	// the administrator's own browser tab (see WithHumanSurface). It is
	// covered by the signature and the owner honors it only for a host access
	// call. Omitted when false, so other requests sign as before.
	HumanSurface bool   `json:"humanSurface,omitempty"`
	Signature    []byte `json:"signature"`
}

// Binding returns the chat binding of the request. Call it only on a request
// that Verify accepted: only then is the binding the signed one.
func (m ForwardedRequest) Binding() (hostops.Binding, bool) {
	if m.Chat == nil || !m.Chat.Valid() {
		return hostops.Binding{}, false
	}
	return *m.Chat, true
}

func (m ForwardedRequest) signingBytes() []byte {
	m.Signature = nil
	b, _ := json.Marshal(m)
	return b
}
func (m *ForwardedRequest) Sign(key ed25519.PrivateKey) {
	m.Signature = ed25519.Sign(key, m.signingBytes())
}

// ForwardVerifier validates the payload independently of the HTTP carrier.
// Replay state is process-local: the carrier must rotate the configured owner
// audience at each owner startup before accepting envelopes. An audience must
// never be reused across verifier instances.
type ForwardVerifier struct {
	OwnerID    string
	Keys       map[string]ed25519.PublicKey
	Revalidate func(context.Context, contract.Actor) error
	mu         sync.Mutex
	seen       map[string]time.Time
}

func (v *ForwardVerifier) Verify(ctx context.Context, peer PeerIdentity, message ForwardedRequest) (contract.Actor, error) {
	denied := func() (contract.Actor, error) { return contract.Actor{}, ErrForbidden }
	now := time.Now()
	key := v.Keys[peer.ID]
	if ctx.Err() != nil || !peer.Verified || peer.ID == "" || peer.ID != message.PeerID || v.OwnerID == "" || message.Audience != v.OwnerID || len(key) != ed25519.PublicKeySize || len(message.Body) > int(contract.DefaultLimits().EnvelopeBytes) || len(message.Body) == 0 || len(message.Nonce) < 1 || len(message.Nonce) > 128 || message.OperationID == "" || len(message.OperationID) > 256 || !message.ExpiresAt.After(now) || message.ExpiresAt.After(now.Add(time.Minute)) || !ed25519.Verify(key, message.signingBytes(), message.Signature) {
		return denied()
	}
	if message.Chat != nil && !message.Chat.Valid() {
		return denied()
	}
	a := message.Actor
	if a.UserID == "" || a.CredentialID == "" || len(a.UserID) > 256 || len(a.CredentialID) > 256 || (a.Mode != "named-user" && a.Mode != "local-owner") || v.Revalidate == nil {
		return denied()
	}
	if err := ClassifyCheck(v.Revalidate(ctx, a)); err != nil {
		return contract.Actor{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seen == nil {
		v.seen = make(map[string]time.Time)
	}
	for nonce, expiry := range v.seen {
		if !expiry.After(now) {
			delete(v.seen, nonce)
		}
	}
	nonce := peer.ID + "\x00" + message.Nonce
	if _, ok := v.seen[nonce]; ok {
		return denied()
	}
	if len(v.seen) >= 10000 {
		return denied()
	}
	v.seen[nonce] = message.ExpiresAt
	return a, nil
}

// VerifierConfig is copied at bootstrap; request callers cannot choose an audience.
type VerifierConfig struct {
	Keys       map[string]ed25519.PublicKey
	Revalidate func(context.Context, contract.Actor) error
}

func NewForwardVerifier(c VerifierConfig) (*ForwardVerifier, error) {
	if len(c.Keys) == 0 || c.Revalidate == nil {
		return nil, ErrForbidden
	}
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	v := &ForwardVerifier{OwnerID: hex.EncodeToString(seed[:]), Keys: make(map[string]ed25519.PublicKey), Revalidate: c.Revalidate}
	for id, key := range c.Keys {
		if id == "" || len(key) != ed25519.PublicKeySize {
			return nil, ErrForbidden
		}
		v.Keys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return v, nil
}
func (v *ForwardVerifier) Audience() string { return v.OwnerID }
