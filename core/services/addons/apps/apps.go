// SPDX-License-Identifier: MIT
// Package apps builds the Apps view of the addons an owner runs: what a
// caller sees of each installed addon, derived only from the verified package
// manifest, the durable lifecycle intent and the process observation.
package apps

import (
	"net/url"
	"slices"
	"sort"
	"strconv"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Snapshot is one addon as the owner knows it. It is input to Build and never
// leaves the owner.
type Snapshot struct {
	AddonID    string
	Installed  bool
	Enabled    bool
	Generation uint64
	// State is the supervisor's observation of the process.
	State   string
	Receipt packages.PackageReceipt
}

// List is the response of the apps list.
type List struct {
	Apps []App `json:"apps"`
}

// StartRequest is the body of the start and stop routes. The generation is
// the one the caller listed: a request for another generation is denied.
type StartRequest struct {
	// OperationID names the lifecycle operation. Repeating it never runs the
	// change twice and, after an outcome_unknown answer, it is the identifier
	// to poll.
	OperationID string `json:"operationId" example:"start-notes-1"`
	// Generation is the installed generation the caller saw in the list.
	Generation uint64 `json:"generation" example:"3"`
}

// MaxTools bounds the tool names of one app.
const MaxTools = 32

// App is what a caller sees of one installed addon. Every field comes from
// the verified manifest, the lifecycle record or the process observation. It
// never carries a package location, a source, an addon endpoint or a
// credential.
type App struct {
	// ID is the addon identifier and Name its stable machine name. They are
	// the same in version one: the receipt records no separate catalog name.
	ID   string `json:"id" example:"notes"`
	Name string `json:"name" example:"notes"`
	// Title and Description are the default-language text of the manifest.
	// Titles and Descriptions carry every language when the manifest
	// localizes the text, keyed by BCP 47 tag; they are absent otherwise.
	Title        string            `json:"title" example:"Notes"`
	Titles       map[string]string `json:"titles,omitempty"`
	Description  string            `json:"description,omitempty" example:"Keep notes."`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	// Icon is a same-origin URL of the verified immutable icon of this
	// generation. It is absent when the manifest declares none.
	Icon    string `json:"icon,omitempty" example:"/api/addons/apps/notes/icon?generation=3"`
	Version string `json:"version" example:"1.2.3"`
	// Generation is the installed generation. A Start or Stop request must
	// carry the value that was listed; a stale value is denied.
	Generation uint64 `json:"generation" example:"3"`
	// Enabled is the durable desired state. Health is State, a separate
	// observation: an enabled app can be failed.
	Enabled bool `json:"enabled"`
	// State is running, starting, failed or stopped. update-pending is not
	// reported in this release: the owner has no authoritative update check.
	State string `json:"state" enums:"running,starting,failed,stopped" example:"running"`
	// MainPage is the path the Open action navigates to. It is present only
	// for an enabled addon whose manifest declares a main page that is one of
	// its pages.
	MainPage string `json:"mainPage,omitempty" example:"/app/addons/notes/home"`
	// HasUI is true when the addon contributes any UI.
	HasUI      bool   `json:"hasUI"`
	LaunchMode string `json:"launchMode" example:"always"`
	// Tools are the tool names of the manifest, at most 32. ToolCount is the
	// full number. The manifest carries no tool descriptions.
	Tools     []string `json:"tools"`
	ToolCount int      `json:"toolCount"`
	// Admin is present only for administrators.
	Admin *AdminView `json:"admin,omitempty"`
}

// AdminView carries the controls only an administrator is shown. The server
// refuses the matching requests of everyone else; this is a convenience for
// the UI, never the control.
type AdminView struct {
	CanStart bool `json:"canStart"`
	CanStop  bool `json:"canStop"`
	// ManagementURL opens the management view of the addon.
	ManagementURL string `json:"managementUrl" example:"/app/addons?manage=notes"`
}

// Build returns the apps a caller may see, sorted by identifier. An uninstalled
// addon is never listed. A disabled one is listed only for an administrator,
// who can start it; for every other caller it does not exist.
func Build(snaps []Snapshot, admin bool) []App {
	out := []App{}
	for _, s := range snaps {
		if !s.Installed || (!s.Enabled && !admin) {
			continue
		}
		out = append(out, build(s, admin))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func build(s Snapshot, admin bool) App {
	m := s.Receipt.Manifest
	id := s.AddonID
	a := App{ID: id, Name: id, Title: id, Version: m.Version, Generation: s.Generation, Enabled: s.Enabled, State: state(s), LaunchMode: contract.LaunchModeAlways, Tools: []string{}}
	if m.LaunchMode != "" {
		a.LaunchMode = m.LaunchMode
	}
	a.ToolCount = len(m.Capabilities.Tools)
	a.Tools = append(a.Tools, m.Capabilities.Tools[:min(a.ToolCount, MaxTools)]...)
	if u := m.Capabilities.UI; u != nil {
		a.HasUI = true
		if u.Title != nil {
			a.Title, a.Titles = u.Title.Default(), u.Title.ByLanguage
		}
		if u.Description != nil {
			a.Description, a.Descriptions = u.Description.Default(), u.Description.ByLanguage
		}
		if s.Receipt.Icon != nil && s.Receipt.Icon.Path == u.Icon && u.Icon != "" {
			a.Icon = "/api/addons/apps/" + url.PathEscape(id) + "/icon?generation=" + strconv.FormatUint(s.Generation, 10)
		}
		// The receipt came from the owner's journal, but the route is
		// re-checked here: an Open action for a page the addon does not
		// declare is never produced.
		if s.Enabled && u.MainPage != "" && slices.Contains(u.Pages, u.MainPage) {
			a.MainPage = "/app/addons/" + url.PathEscape(id) + "/" + url.PathEscape(u.MainPage)
		}
	}
	if admin {
		a.Admin = &AdminView{CanStart: !s.Enabled, CanStop: s.Enabled, ManagementURL: "/app/addons?manage=" + url.QueryEscape(id)}
	}
	return a
}

// state never claims more than was observed: a disabled addon is stopped, and
// an observation the host does not know is failed.
func state(s Snapshot) string {
	if !s.Enabled {
		return "stopped"
	}
	switch s.State {
	case "running", "starting", "failed", "stopped":
		return s.State
	}
	return "failed"
}
