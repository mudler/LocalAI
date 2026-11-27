package plugins

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// ErrNeedsReenable means the install gained code since the user enabled it:
// only the explicit re-enable call (Reenable) turns it on again.
var ErrNeedsReenable = errors.New("plugins: the plugin runs code now; turn it on again to accept that")

// visibleInstall returns the install when the actor can see it: an install
// for everyone, or the actor's own user install. Another user's install is
// ErrNotFound, as if it did not exist.
func (s *Service) visibleInstall(ctx context.Context, a Actor, installID string) (store.Install, error) {
	if a.ID == "" {
		return store.Install{}, ErrForbidden
	}
	in, err := s.getInstall(ctx, installID)
	if err != nil {
		return store.Install{}, err
	}
	if in.Scope == store.ScopeUser && in.Owner != a.ID {
		return store.Install{}, fmt.Errorf("%w: install %q", ErrNotFound, installID)
	}
	return in, nil
}

// SetEnabled turns an install on or off for the actor (spec 12.1). Turning
// it off keeps the actor's settings. Turning on an install that waits for a
// re-enable is ErrNeedsReenable: the user must accept the code first.
func (s *Service) SetEnabled(ctx context.Context, a Actor, installID string, on bool) error {
	return s.saveEnabled(ctx, a, installID, on, false)
}

// Reenable turns on an install whose update added code, for the actor: the
// call is the user's consent to that code.
func (s *Service) Reenable(ctx context.Context, a Actor, installID string) error {
	return s.saveEnabled(ctx, a, installID, true, true)
}

func (s *Service) saveEnabled(ctx context.Context, a Actor, installID string, on, reenable bool) error {
	if _, err := s.visibleInstall(ctx, a, installID); err != nil {
		return err
	}
	us, err := s.st.UserState(ctx, a.ID, installID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		us = store.UserState{Principal: a.ID, InstallID: installID}
	case err != nil:
		return err
	}
	if on && us.NeedsReenable && !reenable {
		return ErrNeedsReenable
	}
	us.Enabled = on
	if on && reenable {
		us.NeedsReenable = false
	}
	us.UpdatedAt = s.now()
	return s.st.SaveUserState(ctx, us)
}
