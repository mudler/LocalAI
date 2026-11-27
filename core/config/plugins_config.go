package config

import "maps"

// PluginsConfig holds the settings of the plugins that are compatible with
// the Claude Code plugin format (spec 6.7). The runtime settings write them;
// the Plugins service reads them at each request, so a change applies at
// once.
type PluginsConfig struct {
	// Marketplaces replaces the default marketplace list at first start
	// (--plugin-marketplaces): comma-separated sources, each optionally
	// "id=source".
	Marketplaces string
	// DisableUserInstalls turns off personal installs of text-only
	// plugins. Stored as the negation so that they are on by default
	// (plugins_user_installs).
	DisableUserInstalls bool
	// ModelTiers maps haiku, sonnet and opus to a model name or alias
	// (plugins_model_tiers).
	ModelTiers map[string]string
	// HookTimeoutSeconds caps one hook handler (plugins_hook_timeout_seconds).
	HookTimeoutSeconds int
	// MaxBytes caps the files of one plugin (plugins_max_bytes).
	MaxBytes int64
}

// Bounds and defaults of the plugin settings.
const (
	DefaultPluginHookTimeoutSeconds       = 60
	MaxPluginHookTimeoutSeconds           = 600
	DefaultPluginMaxBytes           int64 = 20 << 20
	MinPluginMaxBytes               int64 = 1 << 20
	MaxPluginMaxBytes               int64 = 200 << 20
)

// PluginModelTierNames are the keys of plugins_model_tiers.
var PluginModelTierNames = []string{"haiku", "sonnet", "opus"}

// EffectivePluginHookTimeoutSeconds is the hook timeout that applies: the
// setting, or the default when it is unset or out of range.
func (o *ApplicationConfig) EffectivePluginHookTimeoutSeconds() int {
	if n := o.Plugins.HookTimeoutSeconds; n >= 1 && n <= MaxPluginHookTimeoutSeconds {
		return n
	}
	return DefaultPluginHookTimeoutSeconds
}

// EffectivePluginMaxBytes is the size cap of one plugin that applies.
func (o *ApplicationConfig) EffectivePluginMaxBytes() int64 {
	if n := o.Plugins.MaxBytes; n >= MinPluginMaxBytes && n <= MaxPluginMaxBytes {
		return n
	}
	return DefaultPluginMaxBytes
}

// PluginModelTiers returns a copy of the tier map.
func (o *ApplicationConfig) PluginModelTiers() map[string]string {
	out := maps.Clone(o.Plugins.ModelTiers)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

// WithPluginMarketplaces sets the marketplaces that a fresh plugin store
// gets instead of the curated one.
func WithPluginMarketplaces(list string) AppOption {
	return func(o *ApplicationConfig) { o.Plugins.Marketplaces = list }
}
