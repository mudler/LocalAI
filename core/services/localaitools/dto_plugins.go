package localaitools

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Actions of the plugins tool. They share one domain (the marketplaces and
// the installs of plugins in the Claude Code plugin format), so they are one
// tool with an action parameter.
const (
	PluginsActionListMarketplaces   = "list_marketplaces"
	PluginsActionAddMarketplace     = "add_marketplace"
	PluginsActionRefreshMarketplace = "refresh_marketplace"
	PluginsActionBrowse             = "browse"
	PluginsActionListInstalled      = "list_installed"
	PluginsActionInstall            = "install"
	PluginsActionCheckUpdates       = "check_updates"
	PluginsActionEnable             = "enable"
	PluginsActionDisable            = "disable"
	PluginsActionRemove             = "remove"
)

// pluginsMutatingActions change state; the read-only flavour refuses them.
var pluginsMutatingActions = map[string]bool{
	PluginsActionAddMarketplace:     true,
	PluginsActionRefreshMarketplace: true,
	PluginsActionInstall:            true,
	PluginsActionEnable:             true,
	PluginsActionDisable:            true,
	PluginsActionRemove:             true,
}

// PluginsActionConfirms reports whether an action of the plugins tool needs
// the person's confirmation in an assistant chat: it installs, switches or
// removes a plugin, or adds a marketplace that every user then browses.
// refresh_marketplace only reads a marketplace again.
func PluginsActionConfirms(action string) bool {
	return pluginsMutatingActions[action] && action != PluginsActionRefreshMarketplace
}

// PluginsRequest is the input of the plugins tool: one flat object, and the
// handler checks the fields each action needs.
type PluginsRequest struct {
	Action      string `json:"action" jsonschema:"list_marketplaces, add_marketplace, refresh_marketplace, browse, list_installed, install, check_updates, enable, disable or remove"`
	Source      string `json:"source,omitempty" jsonschema:"add_marketplace: owner/repo, an https git URL or an https marketplace.json URL"`
	Ref         string `json:"ref,omitempty" jsonschema:"add_marketplace: branch or tag; empty is the default branch"`
	Marketplace string `json:"marketplace,omitempty" jsonschema:"add_marketplace: the ID to give it; refresh_marketplace: its ID; browse: only this marketplace"`
	Query       string `json:"query,omitempty" jsonschema:"browse: text to match in name, description, category and tags"`
	Plugin      string `json:"plugin,omitempty" jsonschema:"install: name@marketplace from browse"`
	Scope       string `json:"scope,omitempty" jsonschema:"install: user (default, for the caller) or instance (for everyone)"`
	ID          string `json:"id,omitempty" jsonschema:"check_updates, enable, disable, remove: install ID from list_installed"`
	All         bool   `json:"all,omitempty" jsonschema:"list_installed: every user's installs (administrators)"`
}

// PluginMarketplace is a marketplace of plugins.
type PluginMarketplace struct {
	ID          string     `json:"id"`
	Name        string     `json:"name,omitempty"`
	Kind        string     `json:"kind"`
	Location    string     `json:"location"`
	Ref         string     `json:"ref,omitempty"`
	Enabled     bool       `json:"enabled"`
	Default     bool       `json:"default,omitempty"`
	Suspended   bool       `json:"suspended,omitempty"`
	Active      bool       `json:"active"`
	HeadSHA     string     `json:"head_sha,omitempty"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	// LastError is the short code of the last failed refresh.
	LastError string `json:"last_error,omitempty"`
}

// PluginBrowseQuery selects catalog entries.
type PluginBrowseQuery struct {
	Query       string
	Marketplace string
}

// PluginCatalogItem is one entry of a marketplace.
type PluginCatalogItem struct {
	// Plugin is name@marketplace, the ID install takes.
	Plugin      string   `json:"plugin"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Supported   bool     `json:"supported"`
	Reason      string   `json:"reason,omitempty"`
	// RunsCode is known once a version was installed; nil until then.
	RunsCode *bool `json:"runs_code,omitempty"`
	// Installs are the install IDs of this plugin the caller can see.
	Installs []string `json:"installs,omitempty"`
}

// PluginCatalog is one page of the catalog.
type PluginCatalog struct {
	Items []PluginCatalogItem `json:"items"`
	Total int                 `json:"total"`
}

// PluginInstall is an install, with the caller's own switch.
type PluginInstall struct {
	ID       string `json:"id"`
	Plugin   string `json:"plugin"`
	Version  string `json:"version,omitempty"`
	Scope    string `json:"scope"`
	Owner    string `json:"owner,omitempty"`
	SHA      string `json:"sha"`
	RunsCode bool   `json:"runs_code"`
	// State is installed, update_available or blocked.
	State         string `json:"state"`
	UpdateSHA     string `json:"update_sha,omitempty"`
	Enabled       bool   `json:"enabled"`
	NeedsReenable bool   `json:"needs_reenable,omitempty"`
}

// PluginInstallResult is a queued install and what was fetched. The job
// is polled with get_job_status.
type PluginInstallResult struct {
	JobID      string   `json:"job_id"`
	Plugin     string   `json:"plugin"`
	SHA        string   `json:"sha"`
	Digest     string   `json:"digest"`
	Skills     int      `json:"skills"`
	Commands   int      `json:"commands"`
	Agents     int      `json:"agents"`
	MCPServers int      `json:"mcp_servers"`
	Hosts      []string `json:"hosts,omitempty"`
}

// PluginUpdateCheck is the review of an update of one install. It is for
// reading: the assistant does not apply updates.
type PluginUpdateCheck struct {
	Install         string   `json:"install"`
	Plugin          string   `json:"plugin"`
	FromSHA         string   `json:"from_sha"`
	ToSHA           string   `json:"to_sha"`
	Added           []string `json:"added"`
	Removed         []string `json:"removed"`
	Changed         []string `json:"changed"`
	OldCodeReasons  []string `json:"old_code_reasons"`
	NewCodeReasons  []string `json:"new_code_reasons"`
	NeedsCodeReview bool     `json:"needs_code_review"`
	// ReviewURL is where a person applies the update.
	ReviewURL string `json:"review_url"`
}

// PluginState is the caller's switch of an install.
type PluginState struct {
	Install       string `json:"install"`
	Enabled       bool   `json:"enabled"`
	NeedsReenable bool   `json:"needs_reenable,omitempty"`
}

// ErrPluginNeedsBrowser is the refusal of a change that only the
// administrator's own browser session may make: the install of a plugin that
// runs code (spec 9.2). Match it with errors.Is.
var ErrPluginNeedsBrowser = errors.New("plugins: needs the administrator's browser session")

// PluginNeedsBrowserError says which plugin and where a person installs it.
type PluginNeedsBrowserError struct {
	Plugin string
	// ReviewPath is the web UI page that reviews and installs the plugin.
	ReviewPath string
	// Reasons are the code reasons, when known.
	Reasons []string
}

func (e *PluginNeedsBrowserError) Error() string {
	why := "runs code"
	if len(e.Reasons) > 0 {
		why += " (" + strings.Join(e.Reasons, ", ") + ")"
	}
	return fmt.Sprintf("%s %s: installing it needs the administrator's own browser session, which the assistant does not have. "+
		"An administrator reviews and installs it on the web UI page %s.", e.Plugin, why, e.ReviewPath)
}

// Is matches ErrPluginNeedsBrowser.
func (e *PluginNeedsBrowserError) Is(target error) bool { return target == ErrPluginNeedsBrowser }

// ErrPluginNeedsReenable is the refusal to turn on an install that runs code
// since the caller last turned it on. Accepting code in one's chats is a
// person's act on the Plugins tab, not something a model confirms.
var ErrPluginNeedsReenable = errors.New("this plugin runs code since it was last turned on; a person turns it on again on the Plugins tab of the Add-ons page, which accepts the code")
