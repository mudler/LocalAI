package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/studio"
)

// RegisterAddonEvents registers events.emit over the trigger host of the
// chats. It registers nothing without a host operation channel. The trigger
// host is looked up at each call: it is built after the broker, and a server
// with triggers off drops the events.
func (a *Application) RegisterAddonEvents() error {
	if a.addonHostOps == nil {
		return nil
	}
	return hostops.RegisterEvents(a.addonHostOps.Broker, addonEvents{a: a})
}

// addonEvents answers events.emit: the type must be one the add-on's current
// package declares, and the event goes to the owner's watches.
type addonEvents struct{ a *Application }

func (d addonEvents) Emit(ctx context.Context, c hostops.EmitCall) error {
	if d.a.addonManager == nil {
		return fmt.Errorf("%w: add-ons are off", hostops.ErrForbidden)
	}
	decl, ok, err := d.a.addonManager.DeclaredEvent(ctx, c.AddonID, c.Type)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: the add-on does not declare the event type %s", hostops.ErrBadRequest, c.Type)
	}
	h := d.a.StudioTriggers()
	if h == nil {
		return nil // no triggers on this server: nobody watches
	}
	err = h.Emit(ctx, studio.EmitInput{Owner: c.OwnerID, Addon: c.AddonID, Decl: decl, ChatID: c.ChatID, CredentialID: c.CredentialID,
		Payload: c.Payload, DedupeKey: c.DedupeKey})
	if errors.Is(err, studio.ErrEventNotDeclared) {
		return fmt.Errorf("%w: %s", hostops.ErrBadRequest, err.Error())
	}
	return err
}
