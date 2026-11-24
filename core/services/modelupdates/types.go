// Package modelupdates is the update center for gallery models: it finds the
// installed models whose gallery entry changed (the check), builds the read
// model of their states, and runs a staged upgrade with a load check and a
// rollback. Nothing in it upgrades a model without a person asking: models
// have no automatic update path.
//
// Distributed mode. The state lives in the shared models directory: the
// gallery file of each model (origin, hold flag, upgrade journal) and one
// check file (CheckFileName) that the check writes. Every frontend reads the
// same files, so any of them can answer GET /api/models/updates. The check is
// single-runner (a lock, advisorylock.KeyModelUpdateCheck in a cluster); a
// frontend that asks for a check while another runs one waits for its file.
// An upgrade runs on the frontend that took the operation and holds its
// journal; another frontend touches the journal only when it is stale.
package modelupdates

import (
	"time"

	"github.com/mudler/LocalAI/core/services/backendupdates"
)

// KindModel is the kind of every item, so the Updates page can merge the
// model rows with the backend rows.
const KindModel = "model"

// SourceGallery names the one source of model updates: the model galleries.
const SourceGallery = "gallery"

// States of an installed gallery model.
const (
	// StateUpToDate: the gallery entry has not changed since the install.
	StateUpToDate = "up-to-date"
	// StateUpdateAvailable: the entry changed and no key a person edited
	// would be overwritten.
	StateUpdateAvailable = "update-available"
	// StateEdited: the entry changed and the update would overwrite keys a
	// person edited. The upgrade needs a keep-mine or take-gallery choice for
	// each of them (EditedKeys with Conflict set).
	StateEdited = "edited"
	// StateUnknown: the check cannot compare (Reason says why).
	StateUnknown = "unknown"
	// StateGone: no configured gallery lists the entry any more.
	StateGone = "gone"
	// StateHeld: the owner holds this version; the update is not offered.
	StateHeld = "held"
	// StateFailed: the last upgrade did not complete; the old version
	// serves. Upgrade has the error, and a retry is possible.
	StateFailed = "failed"
)

// Reasons of the unknown state.
const (
	// ReasonLegacySidecar: the model was installed by a release that did not
	// record where it came from. A reinstall records it.
	ReasonLegacySidecar = "legacy-sidecar"
	// ReasonUnreadable: the gallery file or the model config cannot be read.
	ReasonUnreadable = "unreadable"
	// ReasonNotChecked: no check has looked at this model yet.
	ReasonNotChecked = "not-checked"
	// ReasonCheckFailed: the check could not read the entry's base config.
	ReasonCheckFailed = "check-failed"
)

// Choices for a key a person edited.
const (
	ChoiceMine    = "mine"
	ChoiceGallery = "gallery"
)

// Snapshot is the answer of GET /api/models/updates. Its shape follows
// backendupdates.Snapshot so the Updates page reads both the same way.
type Snapshot struct {
	// CheckedAt is the end of the last check; zero (omitted) before the
	// first one.
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	// Throttled is set when a check-now call was answered from a check that
	// ended a few seconds before.
	Throttled bool                             `json:"throttled,omitempty"`
	Sources   map[string]backendupdates.Source `json:"sources"`
	Items     []Item                           `json:"items"`
}

// Item is one installed gallery model.
type Item struct {
	// ID is "model:<name>".
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Name is the installed model name.
	Name  string `json:"name"`
	State string `json:"state"`
	// Reason says why the state is unknown.
	Reason string `json:"reason,omitempty"`
	// Error is the error behind a check-failed reason.
	Error string `json:"error,omitempty"`
	// NeedsPerson is true for an update, an edited update and a failed
	// upgrade. Models have no automatic update, so every update needs a
	// person; this is not the backend rule, where the automatic update
	// removes items from the digest.
	NeedsPerson     bool   `json:"needsPerson"`
	Gallery         string `json:"gallery,omitempty"`
	EntryName       string `json:"entryName,omitempty"`
	InstalledDigest string `json:"installedDigest,omitempty"`
	AvailableDigest string `json:"availableDigest,omitempty"`
	Held            bool   `json:"held,omitempty"`
	// ChangedFiles are the files the update adds, replaces or retires.
	ChangedFiles []FileChange `json:"changedFiles,omitempty"`
	// EditedKeys are the keys of the model config a person changed since the
	// install, by dotted path. Conflict marks the ones the update also
	// changes.
	EditedKeys []KeyEdit `json:"editedKeys,omitempty"`
	// ConfigOnly is true when the update changes no file the upgrade must
	// download: only the model config or the prompt templates.
	ConfigOnly bool `json:"configOnly,omitempty"`
	// Upgrade is the last or the running upgrade of this model.
	Upgrade *UpgradeStatus `json:"upgrade,omitempty"`
}

// FileChange is one file an update touches.
type FileChange struct {
	Path string `json:"path"`
	Role string `json:"role"`
	// Change is added, changed or removed.
	Change string `json:"change"`
	// Checksum is false for a file the gallery lists without a sha256. The
	// upgrade refuses to download weights without one.
	Checksum bool `json:"checksum"`
}

// File changes.
const (
	ChangeAdded   = "added"
	ChangeChanged = "changed"
	ChangeRemoved = "removed"
)

// KeyEdit is one key of a model config a person changed.
type KeyEdit struct {
	Key string `json:"key"`
	// Installed is the value the install wrote, Mine the value on disk, and
	// Gallery the value the update would write. A nil value means the key is
	// absent.
	Installed any  `json:"installed,omitempty"`
	Mine      any  `json:"mine,omitempty"`
	Gallery   any  `json:"gallery,omitempty"`
	Conflict  bool `json:"conflict,omitempty"`
}

// UpgradeStatus is the journal of the last or running upgrade.
type UpgradeStatus struct {
	Phase         string    `json:"phase"`
	OpID          string    `json:"opId,omitempty"`
	FromDigest    string    `json:"fromDigest,omitempty"`
	ToDigest      string    `json:"toDigest,omitempty"`
	Error         string    `json:"error,omitempty"`
	Attempts      int       `json:"attempts,omitempty"`
	ConfigOnly    bool      `json:"configOnly,omitempty"`
	RetainedFiles []string  `json:"retainedFiles,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt,omitzero"`
	Running       bool      `json:"running,omitempty"`
	RemovableOld  bool      `json:"removableOld,omitempty"`
	// Note says how the upgrade ran when that differs from what was asked
	// (a canary strategy runs as stop and start until canary is available).
	Note string `json:"note,omitempty"`
}
