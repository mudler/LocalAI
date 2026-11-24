package studio

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
)

// triggersAbsentNote is the line of the turn note for a turn that has none of
// the trigger tools: it names the reason and tells the model not to claim that
// a reminder or a schedule was set. It is empty when a trigger tool is offered,
// so a turn that has them pays nothing. It does not read the message of the
// person: a turn without the tools gets it whatever was asked.
func triggersAbsentNote(built []builtins.Built) string {
	var why string
	seen := false
	for _, id := range triggerToolNames {
		for _, b := range built {
			if b.ID != id {
				continue
			}
			if b.OK {
				return ""
			}
			if !seen || id == proposeTriggerName {
				why, seen = b.Reason, true
			}
		}
	}
	if !seen {
		return ""
	}
	if strings.HasPrefix(why, builtins.ReasonNotPermitted) {
		// A sender who lacks the right is told by the note about the tools held
		// back from them (deniedToolsNote), which names the reason and says not
		// to deny that the tool exists; a second line would only use its room.
		return ""
	}
	return "No reminder or schedule tool this turn: " + absentReason(why) + ". Do not say one was set; tell the person it cannot be done here."
}

// absentReason words a reason of Build for the model.
func absentReason(reason string) string {
	switch {
	case reason == builtins.ReasonOff, reason == "triggers are off on this server":
		return "triggers are switched off on this server"
	case reason == builtins.ReasonChatOff:
		return "triggers are switched off for this chat"
	case reason == builtins.ReasonNotOn:
		return "triggers are not switched on for this chat"
	case reason == ErrTriggersForbidden.Error():
		return "this account does not have the triggers feature"
	case reason == "":
		return "not available"
	}
	return reason
}
