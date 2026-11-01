// SPDX-License-Identifier: MIT
package memory

// Rules describe guards, not promises about a future backend implementation.
var Rules = map[string]string{
	"R01-duplicate":  "existing immutable acceptance identity",
	"R02-stale":      "key, owner, generation or operation mismatch",
	"R02-live":       "recovery arrived before current lease deadline",
	"R02-denied":     "claim belongs to another frontend",
	"R02-expired":    "asynchronous result arrived after operation deadline",
	"R02-unexpected": "event is not enabled in this phase",
	"R02-claim":      "queued capture granted a newer owned fence",
	"R03-revoked":    "current policy removed destination or capability",
	"R03-denied":     "current principal or agent authorization denied",
	"R03-transform":  "authorization granted for summary input",
	"R04-resume":     "retry persists frozen produced payload without transforming again",
	"R04-content":    "authorized content input requires durable payload",
	"R04-summary":    "matching nonempty summary requires durable payload",
	"R04-empty":      "empty input or summary is terminal failure",
	"R04-failed":     "summary failed; original content is not a fallback",
	"R04-durable":    "payload acknowledged; obtain fresh mutation authorization",
	"R05-write":      "current authorization granted for frozen durable payload",
	"R05-saved":      "matching write or reconciliation returned exact entry ID",
	"R05-invalid":    "write receipt omitted exact entry ID",
	"R06-early":      "timeout before operation deadline or recovery before retry due time",
	"R06-wait":       "transient failure or timeout with remaining attempt budget",
	"R06-exhausted":  "attempt budget or capture lifetime exhausted",
	"R06-ready":      "retry due; release to claim queue",
	"R06-recover":    "newer owned claim fence after lease expiry and any retry due time",
	"R07-deleted":    "backend reconciliation returned durable tombstone",
	"R07-absorbed":   "terminal record ignores replay and late results",
	"R08-retry":      "authorized failed retry starts new bounded cycle with same identity and frozen payload",
}
