package studio

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
)

// ownerOf must never turn a lost account into "no user": nil without an error
// means authentication is off, and the tools then run for the single local user.
func TestOwnerOfFailsClosedWhenAuthIsOn(t *testing.T) {
	ctx := context.Background()
	active := &auth.User{ID: "u1", Status: auth.StatusActive}

	off := &Service{}
	if u, err := off.ownerOf(ctx, "anyone"); u != nil || err != nil {
		t.Fatalf("auth off: want (nil, nil), got (%v, %v)", u, err)
	}

	broken := &Service{d: Deps{Users: func(context.Context, string) (*auth.User, error) { return nil, errors.New("db down") }}}
	if u, err := broken.ownerOf(ctx, "u1"); u != nil || err == nil {
		t.Fatalf("lookup error: want an error and no user, got (%v, %v)", u, err)
	}

	missing := &Service{d: Deps{Users: func(context.Context, string) (*auth.User, error) { return nil, nil }}}
	if u, err := missing.ownerOf(ctx, "u1"); u != nil || err == nil {
		t.Fatalf("no user: want an error, got (%v, %v)", u, err)
	}

	ok := &Service{d: Deps{Users: func(context.Context, string) (*auth.User, error) { return active, nil }}}
	if u, err := ok.ownerOf(ctx, "u1"); err != nil || u != active {
		t.Fatalf("found: want the user, got (%v, %v)", u, err)
	}
}
