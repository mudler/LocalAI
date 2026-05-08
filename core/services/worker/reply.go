package worker

import (
	"encoding/json"

	"github.com/mudler/xlog"
)

// replyJSON marshals v to JSON and calls the reply function.
func replyJSON(reply func([]byte), v any) {
	data, err := json.Marshal(v)
	if err != nil {
		xlog.Error("Failed to marshal NATS reply", "error", err)
		data = []byte(`{"error":"internal marshal error"}`)
	}
	reply(data)
}
