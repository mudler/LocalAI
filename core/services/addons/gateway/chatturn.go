// SPDX-License-Identifier: MIT
package gateway

import "context"

type chatTurnKey struct{}

// WithChatTurn marks a context as the call of a tool in a chat turn. In
// local-owner mode a chat turn and the browser share one credential, so the
// credential alone cannot tell them apart; this mark, set by the code that
// runs a model's tool call and read where a proposal is confirmed, does. A
// proposal that waits for a person is never confirmed under it.
func WithChatTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, chatTurnKey{}, true)
}

// IsChatTurn reports whether ctx belongs to the call of a tool in a chat turn.
func IsChatTurn(ctx context.Context) bool {
	v, _ := ctx.Value(chatTurnKey{}).(bool)
	return v
}

type chatConfirmedKey struct{}

// WithChatConfirmed marks a context as the confirmation of a held change that
// the host took from a person in a messaging chat. The host sets it only after
// it checked that the sender holds the confirm capability and is paired with
// the bot, and the model can neither set it nor read it. The owner honors it
// for a proposal that waits only because the connection is set to ask (hold
// write_mode_propose), never for a tool that always asks.
func WithChatConfirmed(ctx context.Context) context.Context {
	return context.WithValue(ctx, chatConfirmedKey{}, true)
}

// ChatConfirmed reports whether WithChatConfirmed marked ctx.
func ChatConfirmed(ctx context.Context) bool {
	v, _ := ctx.Value(chatConfirmedKey{}).(bool)
	return v
}
