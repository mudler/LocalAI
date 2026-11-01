// Where the settings of an add-on live, as pure rules (no React), so the
// Settings page, the Add-ons details and the add-on page shell agree on one
// address for each.
//
// An add-on's settings are drawn on the Settings tab of its details:
// /app/addons?detail=<id>&view=settings. That tab holds its own settings
// section (its settings.sections contribution) and the host controls the
// apps list draws for it (ui.controls; see addons/hostControls.js), such as
// the network of the commands it runs.

export const SETTINGS_VIEW = 'settings'

export function addonSettingsAddress(addonId) {
  return `/app/addons?detail=${encodeURIComponent(addonId)}&view=${SETTINGS_VIEW}`
}

// The old address of the network block of the Settings page. It opens the
// Settings tab of the add-on that draws the network-policy control now.
export const LEGACY_NETWORK_HASH = '#settings-egress'

// The old place of a section was the end of the Settings page, with the
// anchor ad-section-<addon>-<contribution>. An add-on id is lower case letters,
// digits and dashes, and so is a contribution id: the first part that names
// an add-on of the list wins, so "ad-section-web-tools-settings" finds "web"
// only when no "web-tools" add-on is listed.
export function legacySectionTarget(hash, addonIds = []) {
  const m = /^#?ad-section-([a-z0-9-]+)$/.exec(String(hash || ''))
  if (!m) return ''
  const rest = m[1]
  const ids = [...addonIds].sort((a, b) => b.length - a.length)
  const found = ids.find(id => rest === id || rest.startsWith(`${id}-`))
  if (found) return found
  // Not listed (yet): the id up to the last dash, the shape the host wrote.
  const cut = rest.lastIndexOf('-')
  return cut > 0 ? rest.slice(0, cut) : rest
}

// The pane view a search string asks for: the Settings tab only when the
// add-on has one to show.
export function paneView(params, hasSettings) {
  return hasSettings && params.get('view') === SETTINGS_VIEW ? SETTINGS_VIEW : 'overview'
}

