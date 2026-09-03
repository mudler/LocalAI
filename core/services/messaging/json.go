package messaging

import (
	"encoding/json"

	"github.com/mudler/xlog"
)

// SubscribeJSON creates a subscription that automatically unmarshals JSON
// messages. Invalid JSON messages are logged and skipped.
//
// Its parameter is Broadcaster and not MessagingClient, so a call site can be
// moved onto the PostgreSQL carrier without also being rewritten. It lives in
// its own file because client.go is deleted once the request/reply and queue
// halves are retired, and a generic helper with this many call sites must not
// go with it.
func SubscribeJSON[T any](c Broadcaster, subject string, handler func(T)) (Subscription, error) {
	return c.Subscribe(subject, func(data []byte) {
		var evt T
		if err := json.Unmarshal(data, &evt); err != nil {
			xlog.Warn("Failed to unmarshal a broadcast message", "subject", subject, "error", err)
			return
		}
		handler(evt)
	})
}
