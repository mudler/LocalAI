package plugins

import (
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/services/plugins"
)

// Marketplace is a marketplace as the API shows it.
type Marketplace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Location string `json:"location"`
	Ref      string `json:"ref,omitempty"`
	Enabled  bool   `json:"enabled"`
	// Default marks a marketplace that LocalAI ships (the curated one).
	Default bool `json:"default"`
	// Suspended is set for a default marketplace while the "official
	// libraries" switch is off.
	Suspended bool `json:"suspended"`
	// Active is enabled and not suspended: browse and install read it.
	Active      bool       `json:"active"`
	HeadSHA     string     `json:"headSha,omitempty"`
	RefreshedAt *time.Time `json:"refreshedAt,omitempty"`
	// LastError is a short code of the last failed refresh.
	LastError string    `json:"lastError,omitempty"`
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// MarketplaceList is the answer of GET /api/plugins/marketplaces.
type MarketplaceList struct {
	Marketplaces []Marketplace `json:"marketplaces"`
}

func marketplaceOf(v plugins.MarketplaceView) Marketplace {
	m := Marketplace{
		ID: v.ID, Name: v.Name, Kind: v.Kind, Location: v.Location, Ref: v.Ref, Enabled: v.Enabled,
		Default: v.Default, Suspended: v.Suspended, Active: v.Active, HeadSHA: v.HeadSHA,
		LastError: v.LastError, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}
	if !v.RefreshedAt.IsZero() {
		t := v.RefreshedAt
		m.RefreshedAt = &t
	}
	return m
}

// AddMarketplaceRequest adds a marketplace.
type AddMarketplaceRequest struct {
	// Source is owner/repo, an https git URL, or an https URL of a
	// marketplace.json file.
	Source string `json:"source"`
	// Ref is the branch or tag to follow; empty is the default branch.
	Ref string `json:"ref,omitempty"`
	// ID names the marketplace; the default comes from marketplace.json.
	ID string `json:"id,omitempty"`
}

// UpdateMarketplaceRequest changes a marketplace. Absent fields stay.
type UpdateMarketplaceRequest struct {
	Ref     *string `json:"ref,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

// InspectRequest names the plugin to review.
type InspectRequest struct {
	// Plugin is name@marketplace.
	Plugin string `json:"plugin"`
}

// InstallRequest confirms a preview.
type InstallRequest struct {
	Token string `json:"token"`
	// Scope is "user" (the default) or "instance".
	Scope string `json:"scope,omitempty"`
}

// UpdateRequest confirms an update preview.
type UpdateRequest struct {
	Token string `json:"token"`
	// ApprovedFiles names each changed code file when the preview has
	// needsCodeReview.
	ApprovedFiles []string `json:"approved_files,omitempty"`
}

// JobAccepted is the answer of a queued install or update. Its progress is
// in GET /api/operations (isPlugin), and POST
// /api/operations/{jobID}/cancel cancels it.
type JobAccepted struct {
	JobID    string `json:"jobID"`
	PluginID string `json:"plugin"`
}

// InstallList is the answer of GET /api/plugins/installs.
type InstallList struct {
	Installs []plugins.InstallView `json:"installs"`
}

// MyStateRequest changes the caller's own state of an install. Absent
// fields stay.
type MyStateRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
	// Reenable confirms turning on an install that runs code since the
	// caller last turned it on.
	Reenable bool `json:"reenable,omitempty"`
	// Config replaces the non-sensitive settings of the plugin.
	Config json.RawMessage `json:"config,omitempty" swaggertype:"object"`
	// Secrets sets sensitive settings. Not supported yet (422).
	Secrets map[string]string `json:"secrets,omitempty"`
}

// ChatPluginsRequest sets the installs switched off in one chat.
type ChatPluginsRequest struct {
	Off []string `json:"off"`
}

// ChatPluginsResponse is the switched-off list as stored.
type ChatPluginsResponse struct {
	Off []string `json:"off"`
}
