// Where the settings of an add-on live, as pure rules (no React), so the
// Settings page, the Add-ons details and the add-on page shell agree on one
// address for each.
//
// An add-on's own settings (its settings.sections contribution) are drawn on
// the Settings tab of its details: /app/addons?detail=<id>&view=settings.
// Some settings of an add-on's feature are the server's own runtime settings,
// read by LocalAI and not by the add-on (the network of the Sandbox commands,
// which add-on serves a built-in tool). They stay on the Settings page; the
// add-on's Settings tab links to them.

export const SETTINGS_VIEW = 'settings'

export function addonSettingsAddress(addonId) {
  return `/app/addons?detail=${encodeURIComponent(addonId)}&view=${SETTINGS_VIEW}`
}

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

// The server settings that belong to an add-on's feature. The host knows
// these: they are LocalAI runtime settings, documented in runtime-settings.md.
// `anchor` is the id of the block on the Settings page.
export const SERVER_SETTINGS = Object.freeze({
  sandbox: Object.freeze([
    Object.freeze({ id: 'network', group: 'tools', anchor: 'settings-egress', summary: 'egress' }),
    Object.freeze({ id: 'bash', group: 'tools', anchor: 'settings-builtin-tools' }),
  ]),
  web: Object.freeze([
    Object.freeze({ id: 'webTools', group: 'tools', anchor: 'settings-builtin-tools' }),
  ]),
})

export function serverSettingsFor(addonId) {
  return Object.hasOwn(SERVER_SETTINGS, addonId) ? SERVER_SETTINGS[addonId] : []
}

export function serverSettingsAddress(entry) {
  return `/app/settings?group=${encodeURIComponent(entry.group)}#${entry.anchor}`
}

// The pane view a search string asks for: the Settings tab only when the
// add-on has one to show.
export function paneView(params, hasSettings) {
  return hasSettings && params.get('view') === SETTINGS_VIEW ? SETTINGS_VIEW : 'overview'
}

