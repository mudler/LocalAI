// SPDX-License-Identifier: MIT
package lifecycle

var phaseTags = []string{"Idle", "Running", "WaitingApproval", "WaitingAnswer", "Compacting", "Parked", "Stopping", "Interrupted", "Restoring", "WaitingReconciliation", "Deleted"}
var eventKinds = []string{"Submit", "Complete", "CheckpointOK", "CheckpointFailed", "StorageFailed", "ToolRequest", "ToolResult", "Approve", "Deny", "Ask", "Answer", "Compact", "CompactOK", "CompactFailed", "Stop", "Stopped", "Delete", "Restart", "Resume", "Revoke", "LeaseLost", "AcquireLease", "Disconnect", "Reconnect", "Timeout", "RestoreOK", "RestoreFailed", "ReconcileOK"}

func testEvent(s State, kind, id string, over map[string]any) Event {
	i := Identity{Principal: s.Principal, Rev: s.Rev, Epoch: s.Epoch, Attempt: s.Attempt, Turn: turn(s.Phase)}
	request := "request"
	result := "recorded-result"
	var verified *string
	if id == "" && kind != "Submit" {
		id = "request"
	}
	if (kind == "Complete" || kind == "ToolResult") && id != "request" {
		i.Turn = id
	}
	for k, v := range over {
		switch k {
		case "principal":
			i.Principal = v.(string)
		case "rev":
			i.Rev = uint64(v.(int))
		case "epoch":
			i.Epoch = uint64(v.(int))
		case "attempt":
			i.Attempt = uint64(v.(int))
		case "request":
			request = v.(string)
		case "result":
			result = v.(string)
			verified = &result
		}
	}
	switch kind {
	case "Submit":
		return Submit{i, id}
	case "Complete":
		return Complete{i}
	case "CheckpointOK":
		return CheckpointOK{i}
	case "CheckpointFailed":
		return CheckpointFailed{i}
	case "StorageFailed":
		return StorageFailed{i}
	case "ToolRequest":
		return ToolRequest{i, request}
	case "ToolResult":
		return ToolResult{i, request, result}
	case "Approve":
		return Approve{i, request}
	case "Deny":
		return Deny{i, request}
	case "Ask":
		return Ask{i, id}
	case "Answer":
		return Answer{i, id}
	case "Compact":
		return Compact{i}
	case "CompactOK":
		return CompactOK{i}
	case "CompactFailed":
		return CompactFailed{i}
	case "Stop":
		return Stop{i}
	case "Stopped":
		return Stopped{i}
	case "Delete":
		return Delete{i}
	case "Restart":
		return Restart{i}
	case "Resume":
		return Resume{i}
	case "Revoke":
		return Revoke{i}
	case "LeaseLost":
		return LeaseLost{i}
	case "AcquireLease":
		return AcquireLease{i}
	case "Disconnect":
		return Disconnect{i}
	case "Reconnect":
		return Reconnect{i}
	case "Timeout":
		return Timeout{i}
	case "RestoreOK":
		return RestoreOK{i}
	case "RestoreFailed":
		return RestoreFailed{i}
	case "ReconcileOK":
		return ReconcileOK{i, request, verified}
	}
	panic("unknown test event")
}
