// SPDX-License-Identifier: MIT
// Package apps builds the Apps view of the addons an owner runs: what a
// caller sees of each installed addon, derived only from the verified package
// manifest, the durable lifecycle intent and the process observation.
package apps

import (
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

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
	// UpdatePending is true when the owner's cached update check says an
	// update is available for exactly this installed package.
	UpdatePending bool
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
	// State is running, starting, failed or stopped: the observation of the
	// process, never an update.
	State string `json:"state" enums:"running,starting,failed,stopped" example:"running"`
	// Update is update-pending when the owner's cached update check says an
	// update is available for the installed package. It is a separate
	// observation from State and is absent otherwise: an unchecked, failed
	// or up to date add-on has none. The list never reaches a library.
	Update string `json:"update,omitempty" enums:"update-pending" example:"update-pending"`
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
	// ToolDefinitions are the model-facing definitions the manifest declares:
	// the argument schema and the kind of each tool. The host serves them to
	// chat from the verified manifest; the add-on process is never asked.
	ToolDefinitions []contract.ToolDefinition `json:"toolDefinitions,omitempty"`
	// UI describes what the shell loads for this app. It is present only for
	// an enabled addon with a UI whose installed package has a sha256 digest.
	UI *UIView `json:"ui,omitempty"`
	// Admin is present only for administrators.
	Admin *AdminView `json:"admin,omitempty"`
}

// UIView is the registry entry of an addon UI: the same-origin URLs of its
// immutable assets for the installed generation, the SDK versions it accepts
// and the contributions its manifest declares. The page loads nothing from any
// other place, and a URL here stops being served the moment the generation is
// replaced or the addon is disabled.
type UIView struct {
	// Entry is the URL of the packaged ES module. Every asset URL names the
	// package digest, so a new package is a new URL.
	Entry string `json:"entry" example:"/api/addons/notes/assets/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/ui.js"`
	// CSS are the URLs of the declared style sheets.
	CSS []string `json:"css,omitempty"`
	// SDK is the range of host SDK versions the module accepts.
	SDK contract.VersionRange `json:"sdk"`
	// Pages, Navigation and ChatActions are the identifiers the manifest
	// declares. The host decides where each one is shown.
	Pages       []string `json:"pages,omitempty"`
	Navigation  []string `json:"navigation,omitempty"`
	ChatActions []string `json:"chatActions,omitempty"`
	// Actions are the chat actions that declare a placement and a target.
	// Slots and Renderers are the other declared contributions. The manifest
	// was validated at install: every tool named here is a tool of the addon,
	// every slot is a host slot and every icon is a host icon name or the
	// packaged icon. The host draws them only while the addon is enabled and
	// usable by the caller.
	Actions   []ChatActionView    `json:"actions,omitempty"`
	Slots     []SlotView          `json:"slots,omitempty"`
	Renderers []contract.Renderer `json:"renderers,omitempty"`
}

// ChatActionView is a declared chat action as the host draws it.
type ChatActionView struct {
	// ID is the manifest identifier of the action.
	ID string `json:"id" example:"summarize"`
	// Label is the default-language text; Labels carries every language when
	// the manifest localizes it.
	Label  string            `json:"label" example:"Summarize"`
	Labels map[string]string `json:"labels,omitempty"`
	// Icon is a host icon name, or the empty string when the action uses the
	// packaged icon of the app (see the icon of the app).
	Icon string `json:"icon,omitempty" example:"sparkles"`
	// PackagedIcon is true when the action draws the packaged app icon.
	PackagedIcon bool `json:"packagedIcon,omitempty"`
	// Placement is composer or message.
	Placement string `json:"placement" enums:"composer,message" example:"composer"`
	// Tool is the own tool the action calls, or Component the exported
	// component it opens. Exactly one is set.
	Tool      string `json:"tool,omitempty" example:"summarize"`
	Component string `json:"component,omitempty" example:"SummaryPanel"`
}

// SlotView is a component the manifest places in a host slot.
type SlotView struct {
	ID        string `json:"id" example:"notes-nav"`
	Slot      string `json:"slot" enums:"chat.composer.actions,chat.message.actions,app.nav,settings.sections" example:"settings.sections"`
	Component string `json:"component" example:"NotesSettings"`
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
	for _, d := range m.Capabilities.ToolDefinitions {
		d.InputSchema = slices.Clone(d.InputSchema)
		a.ToolDefinitions = append(a.ToolDefinitions, d)
	}
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
		if s.Enabled {
			a.UI = uiView(id, s.Receipt.Digest, u)
		}
	}
	if s.UpdatePending {
		a.Update = "update-pending"
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

var packageDigest = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)

// uiView returns the UI registry entry, or nil when the package digest is not
// a sha256: without it there is no immutable URL to name.
func uiView(id, digest string, u *contract.UI) *UIView {
	m := packageDigest.FindStringSubmatch(digest)
	if m == nil {
		return nil
	}
	base := "/api/addons/" + url.PathEscape(id) + "/assets/" + m[1] + "/"
	asset := func(p string) string {
		parts := strings.Split(p, "/")
		for i := range parts {
			parts[i] = url.PathEscape(parts[i])
		}
		return base + strings.Join(parts, "/")
	}
	v := &UIView{Entry: asset(u.Entry), SDK: u.SDK, Pages: slices.Clone(u.Pages), Navigation: slices.Clone(u.Navigation), ChatActions: u.ChatActionIDs()}
	v.Actions, v.Slots, v.Renderers = contributions(u)
	for _, css := range u.CSS {
		v.CSS = append(v.CSS, asset(css))
	}
	return v
}

// contributions turns the validated declarations into the view the host
// draws. A bare identifier has no placement and is not drawn.
func contributions(u *contract.UI) ([]ChatActionView, []SlotView, []contract.Renderer) {
	var actions []ChatActionView
	for _, a := range u.ChatActions {
		if !a.Declared() || a.Target == nil || a.Label == nil {
			continue
		}
		v := ChatActionView{ID: a.ID, Label: a.Label.Default(), Labels: a.Label.ByLanguage, Placement: a.Placement, Tool: a.Target.Tool, Component: a.Target.Component}
		if a.Icon != "" && a.Icon == u.Icon {
			v.PackagedIcon = true
		} else {
			v.Icon = a.Icon
		}
		actions = append(actions, v)
	}
	var slots []SlotView
	for _, s := range u.Slots {
		slots = append(slots, SlotView{ID: s.ID, Slot: s.Slot, Component: s.Component})
	}
	return actions, slots, slices.Clone(u.Renderers)
}
