package nodes

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// workerBroadcastAllow lists, per node type, the subject filters a worker may
// ask a frontend to publish on its behalf.
//
// An absent node type and an empty list both DENY EVERYTHING. That is the
// opposite of the NATS allow list this replaces, where an empty list meant no
// restriction, and it is the reason deleting an entry here is safe where
// deleting a permissions branch there was a privilege escalation. Nothing may
// be added to this map without a spec that a worker of that type is refused the
// subjects NOT added.
var workerBroadcastAllow = map[string][]string{
	NodeTypeAgent: {
		// The constants and not the strings. A filter written out here is a
		// filter that stops matching the day its builder grows a token, and
		// SubjectMatches compares token counts first, so the drift presents as
		// a worker being refused everything rather than as anything readable.
		messaging.SubjectJobProgressWildcard,
		messaging.SubjectJobResultWildcard,
		messaging.SubjectAgentEventsWildcard,
	},
	// A backend worker asks for no broadcasts. Spelled as an empty list rather
	// than omitted, so the reader sees the decision.
	NodeTypeBackend: {},
}

// MayBroadcast reports whether a worker of nodeType may have subject published
// on its behalf.
func MayBroadcast(nodeType, subject string) bool {
	return mayBroadcastIn(workerBroadcastAllow, nodeType, subject)
}

// mayBroadcastIn is MayBroadcast against a caller-supplied table.
//
// It exists so a spec can hold the table itself constant while varying what is
// IN it, which is the only way to state "an empty list denies" as a property of
// this function rather than as a property of today's entries. Production has
// exactly one table and MayBroadcast is the only way to reach it.
func mayBroadcastIn(table map[string][]string, nodeType, subject string) bool {
	// An empty subject is a line that asked for no broadcast at all, so there
	// is nothing here to authorise. Refusing it is not defensive tidiness: it
	// keeps a filter that happened to match the empty string from turning every
	// ordinary private progress tick into a publish.
	if subject == "" {
		return false
	}
	// A missing node type indexes to nil and the loop then runs zero times,
	// which is the same denial an empty list produces. One code path for both
	// is deliberate: "we have never heard of this worker" and "this worker is
	// allowed nothing" have the same safe answer, and giving them separate
	// branches invites one of the two to acquire an exception.
	for _, filter := range table[nodeType] {
		if messaging.SubjectMatches(filter, subject) {
			return true
		}
	}
	return false
}

// Rebroadcaster turns an allowed progress line into a broadcast.
//
// It holds a Broadcaster and not a MessagingClient because fan-out is all it
// needs and all it may have: a worker asking for a broadcast must not be able
// to reach a request/reply or a queue group through this path.
type Rebroadcaster struct {
	bus messaging.Broadcaster
}

// NewRebroadcaster returns a Rebroadcaster publishing on bus.
func NewRebroadcaster(bus messaging.Broadcaster) *Rebroadcaster {
	return &Rebroadcaster{bus: bus}
}

// The shape of Handle's answer is asserted here, not left to its callers.
//
// A refused or failed re-broadcast must never become an error, because the only
// errors that reach a scheduler from this direction are the worker's own answer
// and an unroutable peer, and a publish failure is neither. Changing the return
// to an error therefore has to fail to COMPILE in the file that states the
// rule, rather than fail a spec somewhere a reviewer might read as a test
// needing an update.
var _ func(string, string, json.RawMessage) bool = (*Rebroadcaster)(nil).Handle

// Handle publishes raw on subject when the worker's type allows it, and returns
// false having published nothing when it does not. It never returns an error
// that a caller could mistake for the worker's answer: a refused or failed
// re-broadcast is logged and the RPC continues, because the RPC's outcome is
// the worker's verdict about the work and a publish failure says nothing about
// it.
//
// raw is published as it arrived. It is a json.RawMessage and every Broadcaster
// encodes what it is given with json.Marshal, which returns a RawMessage
// verbatim, so the subscriber reads the bytes the worker wrote rather than a
// re-encoding of this frontend's idea of them.
func (r *Rebroadcaster) Handle(nodeType, subject string, raw json.RawMessage) bool {
	if !MayBroadcast(nodeType, subject) {
		// Warn rather than Debug: this is a worker asking for something it has
		// no business on, which is the shape of a compromised or mismatched
		// worker and is worth seeing without turning logging up.
		xlog.Warn("refusing a worker's re-broadcast request",
			"nodeType", nodeType, "subject", subject)
		return false
	}
	if r == nil || r.bus == nil {
		xlog.Debug("no broadcaster to re-broadcast a worker's progress line on",
			"nodeType", nodeType, "subject", subject)
		return false
	}
	if err := r.bus.Publish(subject, raw); err != nil {
		xlog.Warn("a worker's re-broadcast could not be published",
			"nodeType", nodeType, "subject", subject, "error", err)
		return false
	}
	return true
}
