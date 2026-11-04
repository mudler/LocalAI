package studio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// BindingCapsMigration is the name of the one-time migration that gives the
// usual permissions to the bindings saved before connector permissions were
// enforced. The record in the store is the only marker: a binding keeps no flag
// of its own, because an empty Caps list is stored the same whether the owner
// never chose or cleared every switch. The migration therefore runs once for
// the whole store, and after it an empty list always means the owner's choice.
const BindingCapsMigration = "binding-caps-usual-v1"

// bindingCapsLease is how long a claim that was never finished blocks another
// frontend. A run takes seconds, so a longer claim means the process died.
const bindingCapsLease = 10 * time.Minute

// BindingCapsReport is the outcome of MigrateBindingCaps.
type BindingCapsReport struct {
	// Ran is false when another process already ran or is running it.
	Ran bool
	// Changed is how many bindings got the usual permissions.
	Changed int
	// Skipped is how many bindings were left as they were: they had a choice
	// already, or no kind, or a kind that declares no permissions.
	Skipped int
}

// MigrateBindingCaps gives the usual permissions (what "Allow the usual" turns
// on, never a sensitive one) to each binding that has no permission at all, once
// per store. kinds are the connector kinds that run now; a binding is changed
// only when its kind is among them and declares permissions. It must be called
// with a non-empty kinds list: with no kind known it would skip every binding
// and still mark the migration done.
//
// It is safe to call from several frontends at once: the store lets one claim
// it, and each binding is written only while it still has no permissions, so an
// owner who saves at the same moment is never overwritten. When it fails it
// releases the claim and the next start tries again. It logs one line for each
// binding changed (ID, kind and permissions, nothing else) and one summary.
func MigrateBindingCaps(ctx context.Context, st connectors.Store, kinds []ConnectorKind, now time.Time, log *slog.Logger) (BindingCapsReport, error) {
	var rep BindingCapsReport
	if st == nil {
		return rep, errors.New("studio: the caps migration needs a store")
	}
	if log == nil {
		log = slog.Default()
	}
	if len(kinds) == 0 {
		return rep, errors.New("studio: the caps migration needs the connector kinds")
	}
	won, err := st.ClaimMigration(ctx, BindingCapsMigration, now, bindingCapsLease)
	if err != nil || !won {
		return rep, err
	}
	rep.Ran = true
	done := false
	defer func() {
		if !done {
			_ = st.ReleaseMigration(context.WithoutCancel(ctx), BindingCapsMigration)
		}
	}()
	usual := contract.UsualConnectorCapabilities()
	all, err := st.AllBindings(ctx)
	if err != nil {
		return rep, fmt.Errorf("list the connector bindings: %w", err)
	}
	for _, b := range all {
		kind, ok := kindForMigration(kinds, b)
		if len(b.Caps) > 0 || b.Kind == "" || !ok || len(kind.Permissions) == 0 {
			rep.Skipped++
			continue
		}
		wrote, err := st.SetCapsIfEmpty(ctx, b.ID, usual)
		if err != nil {
			return rep, fmt.Errorf("give the usual permissions to binding %s: %w", b.ID, err)
		}
		if !wrote {
			rep.Skipped++
			continue
		}
		rep.Changed++
		log.Info("studio: connector binding got the usual permissions", "binding", b.ID, "kind", b.Kind, "caps", usual)
	}
	if err := st.FinishMigration(ctx, BindingCapsMigration, now); err != nil {
		return rep, err
	}
	done = true
	log.Info("studio: connector permissions migration finished", "changed", rep.Changed, "skipped", rep.Skipped)
	return rep, nil
}

// kindForMigration finds the kind of a binding. A binding saved before
// connectors were add-ons names no add-on; it matches a kind ID that only one
// add-on serves.
func kindForMigration(kinds []ConnectorKind, b connectors.Binding) (ConnectorKind, bool) {
	if b.AddonID != "" {
		return kindFor(kinds, b)
	}
	var found []ConnectorKind
	for _, k := range kinds {
		if k.ID == b.Kind {
			found = append(found, k)
		}
	}
	if len(found) != 1 {
		return ConnectorKind{}, false
	}
	return found[0], true
}
