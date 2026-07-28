package openai

import (
	"fmt"
	"regexp"
)

// realtimeDataChannelMaxMessageSize is the SCTP max-message-size LocalAI honors
// for the "oai-events" data channel, in bytes.
//
// Browsers advertise a conservative max-message-size in their SDP offer (Chrome
// uses 262144 = 256 KiB). pion enforces the remote's advertised value on send,
// so a single realtime event larger than it cannot be sent: the SendText fails,
// the event is dropped, and the turn silently yields no response. Some turns
// legitimately produce a single JSON event above 256 KiB (notably tool calls
// with sizeable schemas or results). Browsers advertise this value
// conservatively but their SCTP stacks reassemble much larger messages, so we
// raise the value honored for our own server-generated events.
const realtimeDataChannelMaxMessageSize = 16 * 1024 * 1024 // 16 MiB

var maxMessageSizeAttrRe = regexp.MustCompile(`a=max-message-size:\d+`)

// raiseDataChannelMaxMessageSize rewrites the SCTP max-message-size attribute in
// an SDP offer to realtimeDataChannelMaxMessageSize so pion permits larger
// outbound realtime events. Offers that don't carry the attribute are returned
// unchanged.
func raiseDataChannelMaxMessageSize(sdp string) string {
	return maxMessageSizeAttrRe.ReplaceAllString(sdp, fmt.Sprintf("a=max-message-size:%d", realtimeDataChannelMaxMessageSize))
}
