// The host controls: components LocalAI ships and draws itself, bound to its
// own settings, that an add-on asks for by ID in ui.controls. The server
// lists for each add-on only the controls its grants and tools entitle it to
// (contract.EntitledControls), so the page trusts the apps list and never
// looks at an add-on's ID. Pure rules (no React); the components of each
// control are in hostControlComponents.jsx.
//
// The IDs are the same as pkg/addons/contract/hostcontrols.go; a Go test
// reads this list and keeps both equal.
import { declaresSlot } from './slots.js'
import { usable } from './registryState.js'

export const HOST_CONTROL_IDS = Object.freeze(['network-policy', 'tool-providers'])

export const NETWORK_POLICY = 'network-policy'
export const TOOL_PROVIDERS = 'tool-providers'

// The settings the network-policy control draws on the add-on's Settings tab,
// in order. The Settings page does not draw the ones the control owns.
export const NETWORK_POLICY_FIELDS = Object.freeze([
  'chats_egress_ceiling', 'chats_egress_default', 'chats_egress_ports',
  'chats_egress_extra_registries', 'chats_egress_allowed_hosts', 'chats_egress_audit_days',
])

// The retention also keeps the audit of the commands run on this machine,
// which exists without any add-on: it stays on the Settings page too.
const SHARED = new Set(['chats_egress_audit_days'])

const OWNED = new Set([...NETWORK_POLICY_FIELDS, 'chats_egress_allow_private'].filter(k => !SHARED.has(k)))

// Whether a setting is drawn only by a host control, on an add-on's page.
export function ownedByControl(key) { return OWNED.has(key) }

// The controls the host draws for an app, as the apps list gives them, less
// any ID this page does not know (a newer server).
export function controlsOf(app) {
  const ids = app?.ui?.controls
  return Array.isArray(ids) ? ids.filter(id => HOST_CONTROL_IDS.includes(id)) : []
}

export function hasControl(app, id) { return controlsOf(app).includes(id) }

// Whether the host draws the form that approves an add-on's OAuth login: the
// apps list names a login (app.oauth) for every add-on whose credential slot
// is of kind oauth2, whatever its ID. The add-on lists no control for it.
export function hasOAuthLogin(app) {
  return Boolean(app && app.oauth && typeof app.oauth === 'object')
}

// Whether an add-on's details offer a Settings tab: it declares its own
// settings section, the host draws a control for it, or it logs people in
// with OAuth.
export function hasAddonSettings(app) {
  return Boolean(app && (declaresSlot(app, 'settings.sections') || controlsOf(app).length > 0 || hasOAuthLogin(app)))
}

// The usable add-ons the host draws a control for, in list order.
export function appsWithControl(apps, id) {
  return (apps || []).filter(a => usable(a) && hasControl(a, id))
}
