package studio

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Two connections can hold different token pairs of the same upstream
// account, such as one Slack app registered twice. Both then see every message
// and both answer it. This file finds that case from the identity of the
// upstream account that the add-on reports (contract.ConnectorResponse.AccountID)
// and keeps the younger connection silent.
//
// State mode: shared, and stateless. The identity is stored on the binding and
// the choice of the connection that answers is a function of the stored
// bindings alone (the oldest enabled one), so every frontend reaches the same
// answer for the same message without a lock or a claim. The only memory kept
// in a process is the time of the last attempt to learn an identity, which
// throttles calls and is allowed to differ between frontends.

// DuplicateConfirmation is what a request carries to save an enabled
// connection that is the same bot as another enabled one of its owner.
const DuplicateConfirmation = "answer twice"

// accountRetry is how long a frontend waits before it asks an add-on again for
// the identity of a binding whose add-on did not tell it.
const accountRetry = 10 * time.Minute

// DuplicateRef names the other connection of a bot that is connected twice.
type DuplicateRef struct{ ID, Name string }

// ErrDuplicateConnection means the connection to save is the same bot as
// another enabled connection of its owner, and the request did not confirm.
var ErrDuplicateConnection = errors.New("studio: this bot is already connected")

// DuplicateError is ErrDuplicateConnection with the connection it repeats.
type DuplicateError struct{ Other DuplicateRef }

func (e *DuplicateError) Error() string   { return DuplicateMessage(e.Other.Name) }
func (e *DuplicateError) Is(t error) bool { return t == ErrDuplicateConnection }

// DuplicateMessage is the English sentence for the code CodeDuplicateConnection.
func DuplicateMessage(other string) string {
	return strings.ReplaceAll(blockTable[CodeDuplicateConnection].Message, "{other}", other)
}

// sameBot reports whether a and b are two enabled connections that answer for
// the same upstream account: one owner, one add-on and kind, one known identity.
func sameBot(a, b connectors.Binding) bool {
	return a.ID != b.ID && a.AccountID != "" && a.AccountID == b.AccountID && a.Owner == b.Owner &&
		a.AddonID == b.AddonID && a.Kind == b.Kind &&
		a.Enabled && b.Enabled && !a.CredentialsOnly() && !b.CredentialsOnly()
}

// duplicatesOf lists the bindings of all that are the same bot as b, oldest first.
func duplicatesOf(all []connectors.Binding, b connectors.Binding) []connectors.Binding {
	var out []connectors.Binding
	for _, o := range all {
		if sameBot(o, b) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, olderFirst)
	return out
}

func olderFirst(a, b connectors.Binding) int {
	if c := a.Created.Compare(b.Created); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func refsOf(bs []connectors.Binding) []DuplicateRef {
	if len(bs) == 0 {
		return nil
	}
	out := make([]DuplicateRef, len(bs))
	for i, o := range bs {
		out[i] = DuplicateRef{ID: o.ID, Name: o.Name}
	}
	return out
}

// markDuplicates fills SameBotAs of each info from the bindings of the owner.
func markDuplicates(infos []ConnectorInfo, all []connectors.Binding) {
	for i := range infos {
		infos[i].SameBotAs = refsOf(duplicatesOf(all, infos[i].Binding))
	}
}

// accountOf asks the add-on, with the op me, for the identity of the account
// that a token belongs to. It is empty when the add-on is not running, does
// not tell, or the call fails: the identity is a help, never a reason to
// refuse a token.
func (c *Connectors) accountOf(ctx context.Context, b connectors.Binding, token string) string {
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return ""
	}
	if _, ok := kindFor(kinds, b); !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := c.o.Transport.Call(ctx, verificationScope(b), contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: b.Kind, Token: token})
	if err != nil || resp.Error != nil {
		return ""
	}
	id := strings.TrimSpace(resp.AccountID)
	if len(id) > contract.MaxAccountIDBytes {
		return ""
	}
	return id
}

// learnAccount gives a binding that has no identity the one its add-on
// reports, and stores it. It asks at most once in accountRetry per binding in
// this process, so an add-on that does not tell is not called for every message.
func (c *Connectors) learnAccount(ctx context.Context, b connectors.Binding, token string) connectors.Binding {
	if b.AccountID != "" || token == "" {
		return b
	}
	now := c.o.Clock()
	c.amu2.Lock()
	if last, ok := c.accountTried[b.ID]; ok && now.Sub(last) < accountRetry {
		c.amu2.Unlock()
		return b
	}
	if c.accountTried == nil {
		c.accountTried = map[string]time.Time{}
	}
	c.accountTried[b.ID] = now
	c.amu2.Unlock()
	id := c.accountOf(ctx, b, token)
	if id == "" {
		return b
	}
	if err := c.o.Store.SetAccountID(ctx, b.ID, id); err != nil {
		c.log.Warn("studio: could not store the account of a connection", "binding", b.ID, "error", err)
		return b
	}
	b.AccountID = id
	// A connection that was silent for want of an identity may now be a duplicate.
	c.Notify()
	return b
}

// checkDuplicate refuses to save an enabled connection that is the same bot as
// another enabled one unless the request confirms. old is nil for a new binding;
// a connection that was already a duplicate of the same bots is not asked again.
func checkDuplicate(old *connectors.Binding, b connectors.Binding, mine []connectors.Binding, in ConnectorInput) error {
	dups := duplicatesOf(mine, b)
	if len(dups) == 0 || in.ConfirmDuplicate == DuplicateConfirmation {
		return nil
	}
	if old != nil {
		known := map[string]bool{}
		for _, o := range duplicatesOf(mine, *old) {
			known[o.ID] = true
		}
		if !slices.ContainsFunc(dups, func(o connectors.Binding) bool { return !known[o.ID] }) {
			return nil
		}
	}
	return &DuplicateError{Other: refsOf(dups)[0]}
}

// olderTwin returns the enabled connection of the same bot that must answer in
// place of b, or false when b answers. The oldest enabled connection answers:
// the rule reads the stored bindings only, so every frontend picks the same one.
// It applies to a message that has a platform ID, because that ID is what says
// the two connections were handed the same message. A store that fails leaves
// the message to be answered.
func (c *Connectors) olderTwin(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) (connectors.Binding, bool) {
	if b.AccountID == "" || m.MessageID == "" {
		return connectors.Binding{}, false
	}
	mine, err := c.o.Store.ListBindings(ctx, b.Owner)
	if err != nil {
		return connectors.Binding{}, false
	}
	dups := duplicatesOf(mine, b)
	if len(dups) == 0 || olderFirst(dups[0], b) > 0 {
		return connectors.Binding{}, false
	}
	return dups[0], true
}
