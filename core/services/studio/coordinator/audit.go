package coordinator

import (
	"strings"
	"time"
)

// AuditEntry.Kind values.
const (
	KindContact = "contact"
	KindWake    = "wake"
	KindPause   = "pause"
	KindResume  = "resume"
	KindHelper  = "helper"
)

// AuditEntry.Outcome values.
const (
	OutcomeSent   = "sent"
	OutcomeDenied = "denied"
	OutcomeInfo   = "info"
)

// AuditEntry.Via values: how a contact message travelled.
const (
	ViaInbox     = "inbox"
	ViaConnector = "connector"
)

// AuditEntry.Reason values.
const (
	ReasonPaused         = "paused"
	ReasonNotAContact    = "not_a_contact"
	ReasonBadText        = "bad_text"
	ReasonMinuteCap      = "minute_cap"
	ReasonDayCap         = "day_cap"
	ReasonBindingOff     = "binding_off"
	ReasonBindingBusy    = "binding_busy"
	ReasonInboxOff       = "inbox_off"
	ReasonDeliveryFailed = "delivery_failed"
	ReasonBudget         = "budget"
	ReasonConcurrency    = "concurrency"
	ReasonTimeout        = "timeout"
	ReasonHelperLimit    = "helper_limit"
	ReasonDepthLimit     = "depth_limit"
	ReasonOK             = "ok"
)

// AuditEntry is one line of the audit log: a message sent, an attempt that
// was denied or blocked, a wake, a pause or a helper event. It never holds
// the text of a message, only its length and hash.
type AuditEntry struct {
	ID                       int64
	At                       time.Time
	CoordinatorID, Principal string
	Kind, Outcome, Reason    string
	// Actor is the user who acted, or "coordinator"; Target is the contact or
	// helper ID; Via is "inbox" or "connector".
	Actor, Target, Via string
	Length             int
	Hash               string
}

// Bounds of an audit page.
const (
	DefaultAuditLimit = 50
	MaxAuditLimit     = 500
)

// AuditQuery selects audit entries, newest first. Since is inclusive and Until
// is exclusive. Before is an ID cursor: only entries with a lower ID.
type AuditQuery struct {
	CoordinatorID, Principal, Kind, Outcome string
	Since, Until                            time.Time
	Before                                  int64
	Limit                                   int // 0 = 50, at most 500
}

func (q AuditQuery) limit() int {
	switch {
	case q.Limit <= 0:
		return DefaultAuditLimit
	case q.Limit > MaxAuditLimit:
		return MaxAuditLimit
	}
	return q.Limit
}

// bucketEnd is the end of the period a counter bucket covers, or false for a
// bucket that is not one of the known shapes.
func bucketEnd(bucket string) (time.Time, bool) {
	if rest, ok := strings.CutPrefix(bucket, "min:"); ok {
		t, err := time.Parse("200601021504", rest)
		return t.Add(time.Minute), err == nil
	}
	if _, rest, ok := strings.Cut(bucket, ":"); ok {
		t, err := time.Parse("2006-01-02", rest)
		return t.Add(24 * time.Hour), err == nil
	}
	return time.Time{}, false
}
