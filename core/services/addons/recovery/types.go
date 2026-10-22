// SPDX-License-Identifier: MIT

// Package recovery keeps the data of an add-on recoverable: a consistent
// backup of its files and of the owner's access state, an upgrade that backs
// up before it migrates and never claims that a package rollback restores
// data, an explicit restore that does not give back access that was taken
// away, ownership-mode migration, and the retained-data purge.
//
// It depends on the owner only through the small ports below, so the lifecycle
// manager, the policy store and the tests each provide their own.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Typed errors. Every one of them leaves the data as it was unless its text
// says otherwise.
var (
	ErrBusy                 = errors.New("addon recovery operation already running")
	ErrNotInstalled         = errors.New("addon is not installed")
	ErrInsufficientStorage  = errors.New("not enough free storage for the backup")
	ErrBackupNotFound       = errors.New("backup not found")
	ErrBackupCorrupt        = errors.New("backup failed verification")
	ErrIncompatibleFormat   = errors.New("addon data format is incompatible with the package")
	ErrIdentityChanged      = errors.New("trusted package identity changed")
	ErrAddonRunning         = errors.New("addon must be stopped for this operation")
	ErrMappingInvalid       = errors.New("ownership mapping is invalid")
	ErrMappingRequired      = errors.New("ownership mapping is required")
	ErrDowngradeUnsupported = errors.New("populated named-user data cannot return to local-owner mode")
	// ErrBackupAddonMismatch says a backup belongs to another add-on than the
	// one named in the request. It is an invalid request, never a restore.
	ErrBackupAddonMismatch = fmt.Errorf("%w: the backup belongs to another add-on", ErrInvalid)
	ErrSoleRecoveryCopy    = errors.New("refusing to remove the only verified recovery copy")
	ErrNeedsRecovery       = errors.New("addon data needs recovery")
	ErrOperationConflict   = errors.New("operation ID was used for another request")
	ErrConfirmation        = errors.New("typed confirmation does not match")
	ErrInvalid             = errors.New("invalid recovery request")
	ErrForbidden           = errors.New("addon recovery denied")
	ErrUnavailable         = errors.New("addon recovery unavailable")
)

// Info is what recovery needs to know about one add-on. The host fills it
// from its journal; recovery never reads the journal itself.
type Info struct {
	Installed bool
	// Enabled is the desired state, Running the observation.
	Enabled, Running bool
	Generation       uint64
	// Receipt is the package of the current generation. It is the zero value
	// when the add-on never was installed.
	Receipt packages.PackageReceipt
	// Published lists the generation numbers the journal holds.
	Published []uint64
}

// Quiesced is a stopped add-on that nothing else may start until it is
// resumed or released.
type Quiesced interface {
	// Resume starts the journaled generation again when the add-on is
	// enabled, and lifts the hold either way.
	Resume(ctx context.Context) error
	// Release lifts the hold without starting anything.
	Release()
}

// Host is the lifecycle manager as recovery sees it.
type Host interface {
	Info(ctx context.Context, addonID string) (Info, error)
	// Quiesce drains and stops the add-on and keeps it stopped.
	Quiesce(ctx context.Context, addonID string) (Quiesced, error)
}

// Candidate is a staged package: what it has verified about itself, before it
// is made immutable. packages.StagedPackage is the production one.
type Candidate interface {
	Receipt() packages.PackageReceipt
}

// UpgradeHost adds what an upgrade needs: publishing the staged package and
// starting a candidate generation while the old one is stopped.
type UpgradeHost interface {
	Host
	// Commit makes the staged package immutable and durable.
	Commit(ctx context.Context, staged Candidate) (packages.PackageReceipt, error)
	// StartCandidate starts the package as the given generation and returns
	// once it is ready. A start that fails leaves nothing running.
	StartCandidate(ctx context.Context, addonID string, receipt packages.PackageReceipt, number uint64) error
	// StopCandidate stops the candidate again.
	StopCandidate(ctx context.Context, addonID string) error
	// Publish makes the candidate the current generation, atomically and
	// durably, with the ordering of a plain upgrade.
	Publish(ctx context.Context, addonID string, receipt packages.PackageReceipt, number uint64) error
}

// PolicyExport is the owner's access state of one add-on, as bytes. Recovery
// stores it and hands it back unchanged: only the policy package reads it.
type PolicyExport struct {
	Policy    []byte
	Proposals []byte
}

// RestoreOptions are the inputs of a policy restore.
type RestoreOptions struct {
	// Mapping maps owner identifiers of the backup to current accounts.
	Mapping OwnershipMapping
	// Account says whether an account still exists and may hold access now.
	// A nil error means it does.
	Account func(ctx context.Context, userID string) error
	// Since is when the backup was made: lowerings of access after it are
	// applied to the restored grants.
	Since time.Time
}

// RestoreReport says what a policy restore did.
type RestoreReport struct {
	ResourcesRestored int      `json:"resourcesRestored"`
	ResourcesSkipped  []string `json:"resourcesSkipped,omitempty"`
	GrantsRestored    int      `json:"grantsRestored"`
	GrantsDropped     int      `json:"grantsDropped"`
	// Revoked counts grants dropped or lowered because access was taken away
	// after the backup.
	Revoked int `json:"revoked"`
	// ProposalsCancelled and ProposalsUnknown are the proposals of the add-on
	// that no longer can be confirmed, and the ones whose outcome is looked up
	// by receipt and never applied again.
	ProposalsCancelled int `json:"proposalsCancelled"`
	ProposalsUnknown   int `json:"proposalsUnknown"`
	// HistoryTruncated says the record of lowered access may have lost
	// entries older than the backup, so no grant of the backup was restored.
	HistoryTruncated bool `json:"historyTruncated,omitempty"`
}

// PolicyPort is the owner-local access state.
type PolicyPort interface {
	// Mode is the ownership mode of the store: named-user or local-owner.
	Mode() string
	// Hold stops new admission for the add-on's resources, makes its pending
	// proposals invalid and waits, within a bound, for admitted work. The
	// release function lifts the hold.
	Hold(ctx context.Context, addonID string) (release func(), err error)
	Export(ctx context.Context, addonID string) (PolicyExport, error)
	Restore(ctx context.Context, addonID string, state PolicyExport, o RestoreOptions) (RestoreReport, error)
	// LostWrites counts the operations the owner applied for the add-on after
	// the time, which a restore to an earlier backup loses.
	LostWrites(ctx context.Context, addonID string, since time.Time) (int, error)
	// Forget removes every record of the add-on's resources, as a purge does.
	Forget(ctx context.Context, addonID string) (int, error)
}

// OwnershipMapping maps an old owner identifier to a current account. It is
// explicit: there is no wildcard and no default.
type OwnershipMapping map[string]string

// Retention is the explicit backup retention policy.
type Retention struct {
	// Keep is the number of backups kept per add-on, at least 1.
	Keep int
	// MaxAge removes backups older than it; zero keeps them by count only.
	MaxAge time.Duration
}

// DefaultRetention keeps the three newest backups.
var DefaultRetention = Retention{Keep: 3}

// Actor is the administrator an operation is recorded for.
type Actor = contract.Actor

// Config makes a Recovery.
type Config struct {
	// Root is the owner's storage root. Data is under Root/data/<addon>,
	// backups under Root/backups/<addon> and recovery state under
	// Root/recovery/<addon>.
	Root   string
	Host   Host
	Policy PolicyPort
	// Admin decides whether an actor may run an operation. Nil denies all.
	Admin func(ctx context.Context, actor Actor) error
	// Account is the current account directory, used by a restore.
	Account   func(ctx context.Context, userID string) error
	Retention Retention
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
	// DrainTimeout bounds the wait for admitted work; zero is 15 seconds.
	DrainTimeout time.Duration
	// SelectionsDir is the conversation selection store to include in a
	// backup. Empty skips it.
	SelectionsDir string
}
