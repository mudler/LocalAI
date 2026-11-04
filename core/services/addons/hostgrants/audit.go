// SPDX-License-Identifier: MIT
package hostgrants

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/xlog"
)

// Audit events. Callers must not put secrets or file paths of the policy in
// an entry.
const (
	AuditGrant           = "grant"
	AuditRevoke          = "revoke"
	AuditEdit            = "edit"
	AuditDenyAdd         = "deny-add"
	AuditDenyRemove      = "deny-remove"
	AuditBlanketOn       = "blanket-on"
	AuditBlanketOff      = "blanket-off"
	AuditBlanketGrant    = "blanket-grant"
	AuditRefused         = "refused"
	AuditReconcileRemove = "reconcile-removed"
	AuditRestartForGrant = "restart-for-grant"
)

const maxAuditLine = 16 << 10

// AuditEntry is one line of the audit file.
type AuditEntry struct {
	Time           string   `json:"time"`
	Event          string   `json:"event"`
	AddonID        string   `json:"addon,omitempty"`
	Ops            []string `json:"ops,omitempty"`
	Source         string   `json:"source,omitempty"`
	Version        string   `json:"version,omitempty"`
	Digest         string   `json:"digest,omitempty"`
	OperationID    string   `json:"operationId,omitempty"`
	RevisionBefore uint64   `json:"revisionBefore,omitempty"`
	RevisionAfter  uint64   `json:"revisionAfter,omitempty"`
	// By is the administrator ID, or "deployment" for a switch set by flag.
	By string `json:"by,omitempty"`
	// Credential is the kind of credential used, never the credential.
	Credential string `json:"credential,omitempty"`
	// Reason is the refusal or removal reason, such as not_human_surface,
	// approval_stale, blocked_by_policy, revision_conflict or
	// locked_by_deployment.
	Reason string `json:"reason,omitempty"`
	// Blanket names the switch that made a blanket grant, such as
	// "official@rev3".
	Blanket string `json:"blanket,omitempty"`
}

// Audit is the append-only JSONL file beside the document. Every entry is
// fsynced and also logged as one info line.
type Audit struct {
	mu sync.Mutex
	f  *os.File
}

// OpenAudit opens or creates the audit file for append. A symlink is refused.
func OpenAudit(path string) (*Audit, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600) // #nosec G304 -- fixed file in the guarded owner root; NOFOLLOW rejects symlinks.
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Audit{f: f}, nil
}

// Append writes one entry and syncs it. A nil Audit does nothing, so tests and
// a locked deployment need no special case. The time is set when empty.
func (a *Audit) Append(e AuditEntry) error {
	if a == nil {
		return nil
	}
	if e.Event == "" {
		return invalid("audit event")
	}
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(b) >= maxAuditLine {
		return invalid("audit entry too large")
	}
	b = append(b, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return ErrUnavailable
	}
	// O_APPEND makes the single write atomic with respect to other writers.
	if _, err = a.f.Write(b); err == nil {
		err = a.f.Sync()
	}
	xlog.Info("Addon host grant audit", "event", e.Event, "addon", e.AddonID, "ops", e.Ops, "source", e.Source,
		"operationId", e.OperationID, "by", e.By, "reason", e.Reason, "revision", e.RevisionAfter)
	return err
}

// Close closes the file.
func (a *Audit) Close() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return errors.Join(err)
}
