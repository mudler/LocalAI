package p2prelaymgr

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// RegisterPass implements Manager.
func (m *manager) RegisterPass(ctx context.Context, relayID, code string, ttl time.Duration) error {
	return m.passCall(ctx, relayID, relayproto.OpPass, relayproto.Pass{
		Code: code, TTLSeconds: int(ttl / time.Second),
	})
}

// CancelPass implements Manager.
func (m *manager) CancelPass(ctx context.Context, relayID, code string) error {
	return m.passCall(ctx, relayID, relayproto.OpPassCancel, relayproto.Pass{Code: code})
}

func (m *manager) passCall(ctx context.Context, relayID, op string, p relayproto.Pass) error {
	e, err := m.find(relayID)
	if err != nil {
		return err
	}
	_, err = m.call(ctx, e, relayproto.Request{Op: op, Pass: &p})
	if err != nil && ctx.Err() == nil {
		xlog.Debug("relay pass call failed", "relay", relayID, "op", op, "error", err)
	}
	return err
}

// NewPassRegistrar adapts a Manager to the pairing service. The pairing
// service names a pass by invite and expects the registrar to make up the
// pass code, so the adapter creates a random code per invite and keeps it
// in memory until the invite is cancelled or the pass expires. After a restart
// a pass cannot be cancelled; it ends with its TTL. The map is per instance: in
// a cluster only the frontend that made the invite can cancel its pass, and
// another frontend that cancels the invite leaves the pass to end with its TTL.
func NewPassRegistrar(m Manager) p2ppair.PassRegistrar { return &registrar{m: m} }

type registrar struct {
	m      Manager
	passes passMap
}

func (r *registrar) RegisterPass(ctx context.Context, req p2ppair.PassRequest) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	// The pass is the relay's own secret for this invite; the invite code
	// is not reused, because the server keeps only its hash.
	if err := r.m.RegisterPass(ctx, req.Relay.ID, code, req.TTL); err != nil {
		return "", err
	}
	r.passes.store(req.InviteID, code, time.Now().Add(req.TTL))
	return code, nil
}

func (r *registrar) CancelPass(ctx context.Context, relay p2ppair.Relay, inviteID string) error {
	code, ok := r.passes.load(inviteID, time.Now())
	if !ok {
		return fmt.Errorf("p2prelaymgr: the pass of invite %s is not known any more; it ends with its TTL", inviteID)
	}
	if err := r.m.CancelPass(ctx, relay.ID, code); err != nil {
		return err
	}
	r.passes.delete(inviteID)
	return nil
}
