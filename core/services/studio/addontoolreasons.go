package studio

import (
	"slices"
	"strconv"
	"strings"
)

// The reasons that an add-on tool call records when it does not simply run
// (see addonoutcome.go). The refusals of the permission chain use the codes of
// connectorreasons.go and their sentences; the codes below are the others. A
// code is a stable word; the sentence is for the owner on the connection page
// and stays English here, with the other languages in connectortext.

// toolReasons maps a code to the English sentence. {status} stands for the
// status the service behind the add-on answered.
var toolReasons = map[string]string{
	"needs_confirmation":           "The change is waiting for a person to confirm it.",
	"write_mode_propose":           "The change is waiting for the owner: this connection only proposes changes.",
	"approval_always":              "The change is waiting for the owner: this tool always asks before it runs.",
	"held_by_host":                 "The change is waiting for the owner to confirm it.",
	"write_mode_off":               "This connection does not allow changes. Turn on writes for the connection.",
	"select_connection":            "No connection was chosen for this call. Enable one for the chat or the agent.",
	"no_credential":                "There is no connection with a token for this add-on.",
	"select_resource":              "No resource is selected for this chat.",
	"not_permitted":                "The person who asked is not allowed to use this tool.",
	"resource_not_allowed":         "The resource is not on the list of the connection.",
	"resource_not_in_agent_list":   "The resource is not on the list of the agent.",
	"resource_read_only_for_agent": "The agent may only read this resource.",
	"resource_unreachable":         "The resource does not exist, or the person has no access to it.",
	"invalid_request":              "The add-on refused the request as invalid.",
	"rate_limited":                 "The service asked the caller to wait.",
	"not_running":                  "The add-on was not running, so nothing was sent.",
	"upstream_401":                 "The service refused the token (401). Replace the token or use another connection.",
	"upstream_403":                 "The service refused the call (403): the token lacks a permission. Give the token the permission or use another connection.",
	"upstream_404":                 "The service could not find it (404), or the token cannot see it.",
	"upstream_422":                 "The service did not accept the values of the call (422).",
	"upstream":                     "The service refused the call ({status}).",
	"act_read_only":                "This unattended run was set up to read only.",
	"act_not_granted":              "This unattended run has no permission for the changes of this add-on.",
	"no_grant":                     "No sender of this chat holds a permission now.",
	"invalid_arguments":            "The arguments of the call did not match the tool.",
	"forbidden":                    "The call was not allowed.",
	"unavailable":                  "The add-on could not be reached.",
	"outcome_unknown":              "The add-on did not say whether the change was made.",
	"stale":                        "The target changed after the preview, so nothing was done.",
	"error":                        "The call failed.",
}

// ToolReasonCodes lists the codes that have a sentence of their own, in a
// stable order. The reasons of the permission chain (BlockCodes) are not in
// the list.
func ToolReasonCodes() []string {
	out := make([]string, 0, len(toolReasons))
	for k := range toolReasons {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ToolReasonKey maps the reason of an outcome to the code that has the
// sentence: upstream_<status> keeps its own text for the statuses that have
// one and falls to upstream for the others, a code of the permission chain is
// itself, and a class of failure that has no sentence reads as error.
func ToolReasonKey(reason string) string {
	if _, ok := toolReasons[reason]; ok {
		return reason
	}
	if slices.Contains(BlockCodes(), reason) {
		return reason
	}
	if strings.HasPrefix(reason, "upstream_") {
		return "upstream"
	}
	return "error"
}

// ToolReasonMessage is the English sentence for the reason of an outcome, or
// "" when the outcome has none (it ran).
func ToolReasonMessage(reason string, status int) string {
	if reason == "" {
		return ""
	}
	key := ToolReasonKey(reason)
	if m, ok := toolReasons[key]; ok {
		return strings.ReplaceAll(m, "{status}", strconv.Itoa(status))
	}
	return BlockOwnerMessage(key, "tools.write")
}
