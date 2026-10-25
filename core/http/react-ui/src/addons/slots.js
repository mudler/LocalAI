// The closed host slot registry and the pure rules around it. No React and no
// network: the shell components (AddonSlot.jsx and the host edits) draw what
// this module selects.
//
// A slot is a named place in an existing host page. The host defines the list,
// the props each slot passes and where the contribution is drawn. An add-on
// declares in its manifest which slots it contributes to; nothing is ever
// registered at runtime, and a contribution cannot read host state beyond the
// props listed here. Adding a slot is a host change with its own review.

import { ICON_NAMES } from './sdk.js'
import { invalidReason, usable } from './registryState.js'
import { localizedText } from '../components/addons/addonState.js'

const freeze = (list) => Object.freeze([...list])

// What each slot is, what it passes, and how the host draws it.
export const SLOTS = Object.freeze({
  // Actions in the overflow menu next to the composer's attach buttons.
  'chat.composer.actions': Object.freeze({
    props: freeze(['conversationId', 'disabled']),
    placement: 'composer',
    render: 'menu-item',
  }),
  // Actions in the action row under each message. The props carry no text.
  'chat.message.actions': Object.freeze({
    props: freeze(['conversationId', 'messageId', 'role']),
    placement: 'message',
    render: 'inline',
  }),
  // Entries after the add-on navigation links of the Build hub.
  'app.nav': Object.freeze({ props: freeze([]), placement: '', render: 'nav' }),
  // A titled section at the end of the Settings page, under a host heading.
  'settings.sections': Object.freeze({ props: freeze([]), placement: '', render: 'section' }),
})

export const SLOT_IDS = freeze(Object.keys(SLOTS))

const PLACEMENT_SLOT = Object.freeze({ composer: 'chat.composer.actions', message: 'chat.message.actions' })
const ROLES = new Set(['user', 'assistant', 'system', 'tool'])
const MAX_ID = 128
const MAX_LABEL = 60
const COMPONENT = /^[A-Z][A-Za-z0-9]{0,63}$/
const PROPOSAL_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/
const RESULT_CHARS = 4000

const id = (v) => typeof v === 'string' && v !== '' && v.length <= MAX_ID

// slotProps builds the frozen props of a slot from what the host has at hand.
// Only the contract's names survive, each checked for its shape; anything
// else (message text, history, model, tokens) is dropped, and a value of the
// wrong shape refuses the whole set so a contribution never sees a half of it.
export function slotProps(slot, input) {
  const def = Object.hasOwn(SLOTS, slot) ? SLOTS[slot] : null
  if (!def) return null
  const from = input && typeof input === 'object' ? input : {}
  const out = {}
  for (const name of def.props) {
    const v = from[name]
    if (name === 'conversationId' || name === 'messageId') {
      if (!id(v)) return null
    } else if (name === 'role') {
      if (!ROLES.has(v)) return null
    } else if (name === 'disabled') {
      out.disabled = v === true
      continue
    }
    out[name] = v
  }
  return Object.freeze(out)
}

// The component names the manifest declares: the only names the module may
// supply.
export function declaredComponents(ui) {
  const names = new Set()
  for (const s of ui?.slots || []) if (s && COMPONENT.test(s.component)) names.add(s.component)
  for (const a of ui?.actions || []) if (a && COMPONENT.test(a.component)) names.add(a.component)
  for (const r of ui?.renderers || []) if (r && COMPONENT.test(r.component)) names.add(r.component)
  return names
}

// The label in the viewer's language, then the default text of the server.
export function localizedLabel(action, language) {
  return localizedText({ titles: action?.labels, title: action?.label, id: '' }, 'title', language) || ''
}

function plainLabel(text) {
  return typeof text === 'string' && text.trim() !== '' && text.length <= MAX_LABEL && !/[<>\u0000-\u001f]/.test(text)
}

function actionOf(app, instance, action, language) {
  if (!action || typeof action !== 'object' || PLACEMENT_SLOT[action.placement] === undefined) return null
  const tool = typeof action.tool === 'string' && action.tool !== '' ? action.tool : ''
  const component = typeof action.component === 'string' && action.component !== '' ? action.component : ''
  if ((tool === '') === (component === '')) return null
  if (tool) {
    const tools = Array.isArray(app.tools) ? app.tools : []
    if (!tools.includes(tool) && !(Number(app.toolCount) > tools.length)) return null
  } else if (!COMPONENT.test(component)) {
    return null
  }
  const label = localizedLabel(action, language)
  if (!plainLabel(label) || !/^[a-z][a-z0-9-]{0,63}$/.test(String(action.id))) return null
  let Component = null
  if (component) {
    Component = instance.components && Object.hasOwn(instance.components, component) ? instance.components[component] : null
    if (!Component) return null
  }
  const packaged = action.packagedIcon === true && typeof app.icon === 'string' && app.icon !== ''
  return {
    kind: 'action',
    key: `${app.id}:${action.id}`,
    id: action.id,
    slot: PLACEMENT_SLOT[action.placement],
    addonId: app.id,
    generation: instance.generation,
    addonTitle: localizedText(app, 'title', language) || app.id,
    appIcon: typeof app.icon === 'string' ? app.icon : '',
    label,
    icon: packaged ? '' : (ICON_NAMES.includes(action.icon) ? action.icon : 'puzzle'),
    iconUrl: packaged ? app.icon : '',
    target: tool ? { tool } : { component },
    Component,
    sdk: instance.sdk || null,
  }
}

function componentOf(app, instance, slot, language) {
  if (!slot || typeof slot !== 'object' || !Object.hasOwn(SLOTS, slot.slot) || !COMPONENT.test(slot.component)) return null
  const Component = instance.components && Object.hasOwn(instance.components, slot.component) ? instance.components[slot.component] : null
  if (!Component || !/^[a-z][a-z0-9-]{0,63}$/.test(String(slot.id))) return null
  return {
    kind: 'component',
    key: `${app.id}:${slot.id}`,
    id: slot.id,
    slot: slot.slot,
    addonId: app.id,
    generation: instance.generation,
    addonTitle: localizedText(app, 'title', language) || app.id,
    appIcon: typeof app.icon === 'string' ? app.icon : '',
    Component,
    sdk: instance.sdk || null,
  }
}

// slotContributions returns what the host may draw in `slot` now. A
// contribution exists only while the viewer holds the feature, the server's
// apps list says the add-on is enabled, healthy and compatible, and a ready
// instance of that same generation and package supplied the component. Any
// other state (disabled, crashed, replaced, withdrawn, still loading) is an
// empty list, so one list change unmounts every contribution of an add-on.
export function slotContributions(slot, { apps = [], instances = [], language = 'en', allowed = false } = {}) {
  if (!allowed || !Object.hasOwn(SLOTS, slot)) return []
  const byId = new Map((instances || []).map(i => [i.id, i]))
  const out = []
  for (const app of apps || []) {
    if (!usable(app)) continue
    const instance = byId.get(app.id)
    if (!instance || instance.status !== 'ready' || invalidReason(app, instance)) continue
    for (const action of app.ui.actions || []) {
      const c = actionOf(app, instance, action, language)
      if (c && c.slot === slot) out.push(c)
    }
    for (const s of app.ui.slots || []) {
      if (!s || s.slot !== slot) continue
      const c = componentOf(app, instance, s, language)
      if (c) out.push(c)
    }
  }
  return out
}

// The component the add-on declared for one of its own tools and a renderer
// kind, or null for the host's generic rendering.
export function rendererFor(app, instance, kind, tool) {
  if (!app || !instance || instance.status !== 'ready' || !usable(app) || invalidReason(app, instance)) return null
  const r = (app.ui.renderers || []).find(x => x && x.kind === kind && x.tool === tool)
  if (!r || !COMPONENT.test(r.component)) return null
  return instance.components && Object.hasOwn(instance.components, r.component) ? instance.components[r.component] : null
}

// The arguments of the gateway call a tool action sends: the tool the
// manifest named and the same minimal context a slot gets.
export function actionArguments(contribution, context) {
  const tool = contribution?.target?.tool
  if (!tool) return null
  const c = context && typeof context === 'object' ? context : {}
  const out = {}
  if (id(c.conversationId)) out.conversationId = c.conversationId
  if (id(c.messageId)) out.messageId = c.messageId
  if (ROLES.has(c.role)) out.role = c.role
  return { tool, context: out }
}

// Result text for the host's generic result view: bounded, never markup.
export function resultText(payload) {
  if (payload === undefined || payload === null) return ''
  let text
  try { text = typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2) } catch { return '' }
  if (typeof text !== 'string') return ''
  return text.length > RESULT_CHARS ? `${text.slice(0, RESULT_CHARS)}…` : text
}

// A tool answers with a stored proposal by naming its identifier. The host
// loads the proposal itself; the identifier is the only thing taken.
export function proposalIdOf(payload) {
  const v = payload && typeof payload === 'object' ? payload.proposalId : undefined
  return typeof v === 'string' && PROPOSAL_ID.test(v) ? v : ''
}

export function validProposalId(value) {
  return typeof value === 'string' && PROPOSAL_ID.test(value)
}

// Whether an app declares anything for `slot`: used to decide which instances
// a page needs to hold before it can draw the slot.
export function declaresSlot(app, slot) {
  const ui = app && app.ui
  if (!ui || !Object.hasOwn(SLOTS, slot)) return false
  if ((ui.actions || []).some(a => a && PLACEMENT_SLOT[a.placement] === slot)) return true
  return (ui.slots || []).some(s => s && s.slot === slot)
}
