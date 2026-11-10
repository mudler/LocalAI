package messaging

import (
	"errors"
	"fmt"
)

// MaxBroadcastBytes is the largest encoded payload that a carrier accepts for a
// broadcast. Every carrier enforces the same bound, so a message that one
// carrier carries is not refused by another after a change of carrier.
//
// The bound is far above the payloads that the deployment sends. NATS refuses a
// payload above the max_payload of its server, which is 1 MiB unless an operator
// raised it, and this bound is for the carriers that have no such limit of their
// own.
const MaxBroadcastBytes = 8 << 20

// ErrPayloadTooLarge means a broadcast is larger than MaxBroadcastBytes.
var ErrPayloadTooLarge = errors.New("messaging: broadcast payload is too large")

// CheckBroadcastSize returns an error that wraps ErrPayloadTooLarge when an
// encoded payload of size bytes is above MaxBroadcastBytes.
func CheckBroadcastSize(subject string, size int) error {
	if size > MaxBroadcastBytes {
		return fmt.Errorf("%w: %d bytes on %q, the bound is %d", ErrPayloadTooLarge, size, subject, MaxBroadcastBytes)
	}
	return nil
}
