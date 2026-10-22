// SPDX-License-Identifier: MIT
package apps

import (
	"sort"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Installed is the management view of one installed addon. It is built only for
// administrators from the verified receipt, the lifecycle record and the
// process observation. It never carries a package location, a source address,
// a data path or a credential.
type Installed struct {
	ID          string `json:"id" example:"notes"`
	Title       string `json:"title" example:"Notes"`
	Description string `json:"description,omitempty"`
	// Icon is the same-origin icon URL of the installed generation.
	Icon       string `json:"icon,omitempty"`
	Version    string `json:"version" example:"1.2.3"`
	Generation uint64 `json:"generation" example:"3"`
	// Enabled is the durable desired state. State is the separate observation
	// of the process: running, starting, failed or stopped.
	Enabled bool   `json:"enabled"`
	State   string `json:"state" enums:"running,starting,failed,stopped"`
	// SourceID, Channel, Selection, Verification and Digest say where the
	// package came from and how it was verified. Verification is sha256 or
	// sha256+sigstore.
	SourceID      string        `json:"sourceId,omitempty"`
	Channel       string        `json:"channel,omitempty"`
	Selection     string        `json:"selection,omitempty"`
	Verification  string        `json:"verification"`
	Digest        string        `json:"digest"`
	Platform      Platform      `json:"platform"`
	Compatibility Compatibility `json:"compatibility"`
	Tools         []string      `json:"tools"`
	ToolCount     int           `json:"toolCount"`
	HasUI         bool          `json:"hasUI"`
	// MainPage is present only when the addon is enabled and declares one.
	MainPage   string `json:"mainPage,omitempty"`
	LaunchMode string `json:"launchMode"`
	// Generations is the number of retained installed generations.
	Generations int `json:"generations"`
	// Restarts is the number of restart attempts the supervisor made.
	Restarts int     `json:"restarts"`
	Storage  Storage `json:"storage"`
	// Update is present only when the owner has an authoritative update check
	// for this addon. It carries the state of the check, so a failed check is
	// never read as up to date.
	Update *Update `json:"update,omitempty"`
	// Recovery is the recovery state of the add-on's data when it is not
	// ready: upgrading, restoring, needs-recovery or purging. An add-on in
	// needs-recovery is not started until an administrator restores a backup
	// or purges its data. It is absent when the data is ready.
	Recovery string `json:"recovery,omitempty" example:"needs-recovery"`
}

// Platform is the operating system and architecture the package was built for.
type Platform struct {
	OS   string `json:"os" example:"linux"`
	Arch string `json:"arch" example:"amd64"`
}

// Compatibility is what the package manifest declares it works with.
type Compatibility struct {
	Host       contract.VersionRange `json:"host"`
	API        contract.VersionRange `json:"api"`
	Protocol   contract.VersionRange `json:"protocol"`
	DataFormat contract.VersionRange `json:"dataFormat"`
}

// Storage is the size of the retained data of the addon. Truncated is true
// when the bounded count stopped early.
type Storage struct {
	Bytes     int64 `json:"bytes"`
	Truncated bool  `json:"truncated"`
}

// Update is an authoritative update check result of the owner.
type Update struct {
	// Available is true only for the state update-available.
	Available bool `json:"available"`
	// State is unknown, up-to-date, update-available, incompatible,
	// check-failed or integrity-error.
	State string `json:"state" enums:"unknown,up-to-date,update-available,incompatible,check-failed,integrity-error" example:"update-available"`
	// Version and Digest are the candidate the check resolved.
	Version string `json:"version,omitempty" example:"1.3.0"`
	Digest  string `json:"digest,omitempty"`
	// CheckedAt is when the status was computed and LastSuccess when the
	// library was last read successfully, both RFC 3339.
	CheckedAt   string `json:"checkedAt,omitempty"`
	LastSuccess string `json:"lastSuccess,omitempty"`
	// Stale is true when the answer rests on data older than the time to live.
	Stale bool `json:"stale,omitempty"`
	// Reasons say which range an incompatible candidate does not fit.
	Reasons []string `json:"reasons,omitempty" example:"host-version"`
	// Error is the redacted class of a failed check.
	Error string `json:"error,omitempty" example:"refresh-failed"`
}

// BuildInstalled builds the management view of the installed addons, sorted by
// identifier. Restarts and storage are filled by the caller.
func BuildInstalled(snaps []Snapshot) []Installed {
	out := []Installed{}
	for _, s := range snaps {
		if !s.Installed {
			continue
		}
		a := build(s, true)
		m, r := s.Receipt.Manifest, s.Receipt
		out = append(out, Installed{
			ID: s.AddonID, Title: a.Title, Description: a.Description, Icon: a.Icon, Version: m.Version, Generation: s.Generation,
			Enabled: s.Enabled, State: a.State, SourceID: r.SourceID, Channel: r.Channel, Selection: r.Selection, Verification: r.Verification, Digest: r.Digest,
			Platform:      Platform{OS: m.OS, Arch: m.Arch},
			Compatibility: Compatibility{Host: m.Host, API: m.API, Protocol: m.Protocol, DataFormat: m.DataFormat},
			Tools:         a.Tools, ToolCount: a.ToolCount, HasUI: a.HasUI, MainPage: a.MainPage, LaunchMode: a.LaunchMode,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
