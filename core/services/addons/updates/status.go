// SPDX-License-Identifier: MIT
// Package updates is the owner's authoritative update check of installed
// add-ons. It compares what is installed with what the library the add-on came
// from offers now, and says so with a state, never with a guess. It applies
// nothing: an upgrade is a separate explicit lifecycle command.
package updates

import (
	"errors"
	"time"
)

// State is the answer of an update check.
type State string

const (
	// Unknown means no check ran for the installed package.
	Unknown State = "unknown"
	// UpToDate means the library offers nothing newer for the selection the
	// add-on was installed from.
	UpToDate State = "up-to-date"
	// UpdateAvailable means a candidate exists, is compatible and differs.
	UpdateAvailable State = "update-available"
	// Incompatible means a candidate exists that this host cannot run. The
	// reasons say which range failed.
	Incompatible State = "incompatible"
	// CheckFailed means the check could not tell: the library could not be
	// read or does not list the add-on. It never reads as up to date.
	CheckFailed State = "check-failed"
	// IntegrityError means the library lists the installed immutable version
	// with another digest. It is never an update.
	IntegrityError State = "integrity-error"
)

// Compatibility reasons of an Incompatible status.
const (
	ReasonPlatform   = "platform"
	ReasonHost       = "host-version"
	ReasonAPI        = "api-version"
	ReasonProtocol   = "protocol-version"
	ReasonSDK        = "sdk-version"
	ReasonDataFormat = "data-format"
	// ReasonDigestChanged is the reason of an IntegrityError.
	ReasonDigestChanged = "digest-changed"
)

// Error classes of a CheckFailed status. They carry no address, credential or
// library text: they only name the kind of failure.
const (
	ClassSource     = "source-unavailable"
	ClassRefresh    = "refresh-failed"
	ClassCredential = "credential-unavailable" // #nosec G101 -- a name of an error class or a fixture value, not a credential
	ClassNotListed  = "not-listed"
	ClassAmbiguous  = "ambiguous-release"
	ClassCanceled   = "canceled"
	ClassInternal   = "internal"
)

// Status is the update check result of one installed add-on.
type Status struct {
	AddonID string `json:"addonId" example:"notes"`
	State   State  `json:"state" enums:"unknown,up-to-date,update-available,incompatible,check-failed,integrity-error" example:"update-available"`
	// Available is true only for update-available.
	Available bool `json:"available"`
	// Generation is the installed generation the status describes.
	Generation       uint64 `json:"generation" example:"1"`
	InstalledVersion string `json:"installedVersion,omitempty" example:"1.0.0"`
	InstalledDigest  string `json:"installedDigest,omitempty"`
	CandidateVersion string `json:"candidateVersion,omitempty" example:"1.1.0"`
	CandidateDigest  string `json:"candidateDigest,omitempty"`
	// SourceID, Channel and Selection are the library, channel and
	// selection the add-on was installed from. They never change by themselves.
	SourceID  string `json:"sourceId,omitempty" example:"official"`
	Channel   string `json:"channel,omitempty" example:"development"`
	Selection string `json:"selection,omitempty" example:"1.0.0"`
	// CheckedAt is when the status was computed. LastSuccess is when the
	// library was last read successfully: a failed check keeps it, so the
	// page can say how old the knowledge is.
	CheckedAt   time.Time `json:"checkedAt,omitzero"`
	LastSuccess time.Time `json:"lastSuccess,omitzero"`
	// Stale is true when the library data or the cached answer is older than
	// the time to live.
	Stale bool `json:"stale,omitempty"`
	// Throttled is true when an explicit refresh was not performed because
	// one ran moments ago. The status is then the last one.
	Throttled bool `json:"throttled,omitempty"`
	// Reasons are the compatibility reasons of an incompatible or
	// integrity-error status.
	Reasons []string `json:"reasons,omitempty"`
	// Error is the redacted class of a failed check.
	Error string `json:"error,omitempty" example:"refresh-failed"`
}

// Errors the checker returns. A failed check is not one of them: it is a status.
var (
	ErrNotInstalled = errors.New("add-on not installed")
	ErrUnavailable  = errors.New("update check unavailable")
)

// Redacted is the view a caller who is not an administrator may read: the
// state, the versions and the times. It names no library, channel, selection
// or digest.
func (s Status) Redacted() Status {
	s.InstalledDigest, s.CandidateDigest = "", ""
	s.SourceID, s.Channel, s.Selection = "", "", ""
	return s
}
