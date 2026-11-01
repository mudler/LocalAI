// SPDX-License-Identifier: MIT
package hostops

import "context"

// maxBindingField bounds each field of a binding, as the forwarded request
// bounds the actor's.
const maxBindingField = 256

// Binding names the chat an add-on call was made for. The host keeps it next
// to the envelope of the live request (InFlight) and, between frontends, in the
// signed forwarded request. It is never part of the envelope the add-on
// receives: installed add-ons decode the envelope with unknown fields refused.
// The workspace operations of a bound call act with the chat's access, so a
// chat whose agent has a folder reaches only that folder through an add-on.
type Binding struct {
	// Principal is the owner of the chat's files, as the chats service names
	// the user. It must be the user the add-on actor maps to.
	Principal string `json:"principal"`
	ChatID    string `json:"chatId"`
}

// Valid reports whether both fields are set and bounded.
func (b Binding) Valid() bool {
	return b.Principal != "" && b.ChatID != "" && len(b.Principal) <= maxBindingField && len(b.ChatID) <= maxBindingField
}

type bindingKey struct{}

// WithBinding returns a context that carries the chat binding of a call. Only
// host code sets it: the chat turn that runs an add-on tool, and the owner when
// it serves a forwarded request whose signature covers the binding.
func WithBinding(ctx context.Context, b Binding) context.Context {
	return context.WithValue(ctx, bindingKey{}, b)
}

// BindingOf returns the chat binding a context carries. An incomplete binding
// is no binding.
func BindingOf(ctx context.Context) (Binding, bool) {
	b, ok := ctx.Value(bindingKey{}).(Binding)
	if !ok || !b.Valid() {
		return Binding{}, false
	}
	return b, true
}
