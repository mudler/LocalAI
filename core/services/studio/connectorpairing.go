package studio

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Sender pairing proves that the owner of a binding controls a platform
// account. The owner asks the host for a code, then sends it as a direct
// message to the bot from that account. The host checks the code before any
// model turn, so the code never reaches a model or the sender log. Pairing
// needs only a stable sender ID, so it works for every connector add-on.

const (
	pairingPrefix = "LA-"
	pairingChars  = 8
	// pairingAlphabet has no look-alike characters (0/O, 1/I/L).
	pairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	// pairingAttempts is how many wrong codes a sender may send in an hour
	// before the host stops looking at that sender's messages for an hour.
	pairingAttempts = 5
	pairingWindow   = time.Hour

	sentPaired        = "Paired. This account can now use the sensitive features that you enabled for this bot."
	sentPairingBad    = "That code is not valid. It may have expired or been used. Ask for a new one."
	sentPairingFull   = "This bot already has the most paired accounts. Remove one in the settings, then pair again."
	sentPairingDirect = "Pairing works in a direct message only."
)

var pairingShape = regexp.MustCompile(`^LA-[A-Z0-9]{8}$`)

// PairingCode is a code for a sender to send to the bot.
type PairingCode struct {
	Code    string
	Expires time.Time
}

func pairingHash(binding, code string) string {
	sum := sha256.Sum256([]byte(binding + "\x00" + strings.ToUpper(code)))
	return hex.EncodeToString(sum[:])
}

func newPairingCode() (string, error) {
	var raw [pairingChars]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(pairingPrefix)
	for _, x := range raw {
		b.WriteByte(pairingAlphabet[int(x)%len(pairingAlphabet)])
	}
	return b.String(), nil
}

// StartPairing makes a code for a binding of the user. It is valid for
// connectors.PairingCodeTTL and works once. A new code replaces the old one.
func (c *Connectors) StartPairing(ctx context.Context, user, id string) (PairingCode, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return PairingCode{}, err
	}
	if b.CredentialsOnly() {
		return PairingCode{}, connectorBad("pairing needs an agent: a connection without an agent receives no messages")
	}
	code, err := newPairingCode()
	if err != nil {
		return PairingCode{}, err
	}
	exp := c.o.Clock().UTC().Add(connectors.PairingCodeTTL)
	if err := c.o.Store.PutPairing(ctx, b.ID, connectors.Pairing{Hash: pairingHash(b.ID, code), Expires: exp}); err != nil {
		return PairingCode{}, err
	}
	c.audit(b, "", "pairing.code", "issued")
	return PairingCode{Code: code, Expires: exp}, nil
}

// RemovePaired ends the pairing of a sender of a binding of the user. It is
// not an error when the sender is not paired.
func (c *Connectors) RemovePaired(ctx context.Context, user, id, senderID string) (ConnectorInfo, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return ConnectorInfo{}, err
	}
	b.Paired = slices.DeleteFunc(b.Paired, func(p connectors.PairedSender) bool { return p.SenderID == senderID })
	b.Updated = c.o.Clock().UTC()
	if err := c.o.Store.PutBinding(ctx, b); err != nil {
		return ConnectorInfo{}, err
	}
	c.audit(b, senderID, "pairing.remove", "removed")
	return c.withState(ctx, info(b)), nil
}

// audit records a pairing decision. It never carries a code.
func (c *Connectors) audit(b connectors.Binding, sender, event, result string) {
	c.log.Info("studio: connector audit", "binding", b.ID, "kind", b.Kind, "sender", sender, "event", event, "result", result)
}

// pairingLocked tells whether the sender is locked out: pairingAttempts wrong
// codes in pairingWindow lock the sender for pairingWindow.
func (c *Connectors) pairingLocked(s *senderState) bool {
	now := c.o.Clock()
	c.smu.Lock()
	defer c.smu.Unlock()
	return now.Before(s.lockedUntil)
}

// pairingFailed counts a wrong code of a sender.
func (c *Connectors) pairingFailed(s *senderState) {
	now := c.o.Clock()
	c.smu.Lock()
	defer c.smu.Unlock()
	kept := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < pairingWindow {
			kept = append(kept, t)
		}
	}
	s.failures = append(kept, now)
	if len(s.failures) >= pairingAttempts {
		s.lockedUntil = now.Add(pairingWindow)
		s.failures = nil
	}
}

// tryPairing handles a message that has the shape of a pairing code. It
// returns true when the message was a pairing attempt: the caller must not
// start a turn for it. The sender is already on the allowlist.
func (c *Connectors) tryPairing(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, st *senderState, say func(string)) bool {
	code := strings.ToUpper(strings.TrimSpace(m.Text))
	if !pairingShape.MatchString(code) {
		return false
	}
	if c.pairingLocked(st) {
		c.audit(b, m.SenderID, "pairing.attempt", "locked")
		return true
	}
	if !m.Direct {
		// A code written in a group is spent in public: it is not tried.
		c.audit(b, m.SenderID, "pairing.attempt", "not direct")
		c.pairingFailed(st)
		say(sentPairingDirect)
		return true
	}
	ok, err := c.o.Store.ConsumePairing(ctx, b.ID, pairingHash(b.ID, code), c.o.Clock())
	if err != nil {
		c.log.Warn("studio: could not check a pairing code", "binding", b.ID, "error", err)
		say(sentUnavailable)
		return true
	}
	if !ok {
		c.audit(b, m.SenderID, "pairing.attempt", "refused")
		c.pairingFailed(st)
		say(sentPairingBad)
		return true
	}
	if err := c.addPaired(ctx, b.ID, m.SenderID); err != nil {
		c.audit(b, m.SenderID, "pairing.attempt", err.Error())
		say(sentPairingFull)
		return true
	}
	c.audit(b, m.SenderID, "pairing.attempt", "paired")
	say(sentPaired)
	return true
}

// addPaired stores a pairing, dropping the ones that ended. A sender who pairs
// again renews the pairing.
func (c *Connectors) addPaired(ctx context.Context, id, senderID string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	b, err := c.o.Store.GetBinding(ctx, id)
	if err != nil {
		return err
	}
	if b.CredentialsOnly() {
		return connectors.ErrForbidden
	}
	now := c.o.Clock().UTC()
	b.Paired = slices.DeleteFunc(b.Paired, func(p connectors.PairedSender) bool {
		return p.SenderID == senderID || now.Sub(p.At) >= connectors.PairedTTL
	})
	if len(b.Paired) >= connectors.MaxPaired {
		return errPairedFull
	}
	b.Paired = append(b.Paired, connectors.PairedSender{SenderID: senderID, At: now})
	b.Updated = now
	return c.o.Store.PutBinding(ctx, b)
}

type pairingErr string

func (e pairingErr) Error() string { return string(e) }

const errPairedFull = pairingErr("too many paired senders")
