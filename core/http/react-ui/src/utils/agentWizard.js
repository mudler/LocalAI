// The new-agent wizard: its steps, the form it starts from, the body it sends,
// and the list of apps its third step offers. No React, so a node test drives it.
import { agentPayload, newAgent } from './chatAgents.js'
import { AGENT_TEMPLATES } from './agentConfigTools.js'
import { addonPageAddress } from '../addons/addresses.js'

export const WIZARD_STEPS = ['purpose', 'abilities', 'where', 'review']

// The form a new agent starts from. It is the form the full editor starts a new
// agent from, so skipping every optional step sends what that form sends.
// A starting point fills the text fields only; it never changes the tools.
export function wizardForm(templateId) {
  const form = newAgent()
  const tpl = AGENT_TEMPLATES.find((x) => x.id === templateId)
  return tpl ? { ...form, name: tpl.name, description: tpl.description, instructions: tpl.instructions } : form
}

// The body of the create request: the one the full editor sends for a new agent.
export const wizardPayload = (form) => agentPayload(form, { initialFolders: [] })

// Only a name and instructions are required.
export function purposeReady(form) {
  return Boolean((form?.name || '').trim() && (form?.instructions || '').trim())
}

// A step is reachable when every step before it that has a requirement is met.
export function stepReachable(step, form) {
  return step === 'purpose' || purposeReady(form)
}

const list = (x) => (Array.isArray(x) ? x : [])
const text = (...xs) => xs.find((x) => typeof x === 'string' && x.trim()) || ''

// What the server calls the setup text of an app: a short sentence from the
// add-on itself. Empty when the add-on declares none.
export const setupOf = (x) => text(x?.setup)

// A connector kind of a running add-on. Kinds that hold credentials only
// (they can have no agent) give an agent tools, not a place to answer in.
export function kindItem(k) {
  const credentialsOnly = Boolean(k.agent_optional ?? k.agentOptional)
  return {
    key: `kind:${k.addon}/${k.kind}`,
    type: credentialsOnly ? 'tool' : 'kind',
    addon: k.addon,
    kind: k.kind,
    page: k.page || '',
    title: text(k.localized_title, k.title, k.kind),
    description: text(k.localized_description, k.description),
    setup: setupOf(k),
    credentialed: credentialsOnly,
    installed: true,
  }
}

export function toolItem(a) {
  return {
    key: `tool:${a.id}`,
    type: 'tool',
    addon: a.id,
    kind: '',
    page: '',
    title: text(a.title, a.id),
    description: text(a.description),
    setup: setupOf(a),
    credentialed: Boolean(a.credentialed),
    installed: true,
  }
}

export function catalogItem(e) {
  return {
    key: `catalog:${e.id}`,
    type: 'catalog',
    addon: e.id,
    kind: '',
    page: '',
    title: text(e.title, e.id),
    description: text(e.description),
    setup: setupOf(e),
    credentialed: false,
    installed: false,
  }
}

// The apps the third step offers, in two groups, built only from what the
// server reports: the kinds of running connector add-ons, the add-ons that give
// tools, and the installable add-ons of the catalog. An add-on that shows once
// is not shown again under another source. Nothing names an add-on.
export function whereItems({ kinds, toolAddons, catalog }) {
  const chat = []
  const tools = []
  const seen = new Set()
  for (const k of list(kinds)) {
    const it = kindItem(k)
    if (seen.has(it.key)) continue
    seen.add(it.key)
    ;(it.type === 'kind' ? chat : tools).push(it)
  }
  // An add-on that is already a chat app or a credentials entry is not repeated.
  const shownAddons = new Set([...chat, ...tools].map((x) => x.addon))
  for (const a of list(toolAddons)) {
    if (a.gone || shownAddons.has(a.id)) continue
    const it = toolItem(a)
    shownAddons.add(a.id)
    tools.push(it)
  }
  for (const e of list(catalog)) {
    if (!e?.id || shownAddons.has(e.id)) continue
    shownAddons.add(e.id)
    tools.push(catalogItem(e))
  }
  return { chat, tools }
}

// The line under an app: what it needs. The add-on's own text, else a generic
// line the caller words ("Finish setting up {title}").
export const needsLine = (item, generic) => item.setup || generic(item.title)

// The address a not-installed add-on opens: its entry in the catalog.
export const catalogAddress = (addon) => `/app/addons?tab=browse&q=${encodeURIComponent(addon)}`

// The page of a chat app, opened on the add sheet with this agent chosen.
export function kindSetupAddress(item, agentId) {
  const search = `new=1&agent=${encodeURIComponent(agentId)}`
  return item.page
    ? addonPageAddress({ addon: item.addon, page: item.page, search })
    : `/app/addons?detail=${encodeURIComponent(item.addon)}`
}

// What the apps do to the summary rail and the review.
export const chosenItems = (groups, keys) => [...groups.chat, ...groups.tools].filter((x) => keys.includes(x.key))
