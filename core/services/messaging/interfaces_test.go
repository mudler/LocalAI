package messaging_test

import (
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// Compile-time conformance. A wide client is still a Broadcaster, and so is the
// in-memory double every cross-replica spec shares.
var (
	_ messaging.Broadcaster     = messaging.MessagingClient(nil)
	_ messaging.MessagingClient = (*messaging.Client)(nil)
	_ messaging.Broadcaster     = (*testutil.FakeBus)(nil)
)
