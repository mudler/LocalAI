// Pure rules of the slots of ui.json: small places of the app (the chat
// header, the sidebar footer, the composer toolbar) that show page tiles, tool
// buttons and commands. No React, no network, no DOM: the slot bar, the Slots
// tab and the unit tests use the same functions. The server validates the file
// with the same rules (core/services/studio/wellknown/schema.go, slotErrors).

import { TOOL_NAME_RE, commandNameOk } from './userCommands.js'

export const SLOT_NAMES = ['chatHeader', 'sidebarFooter', 'composerToolbar']
export const SLOT_CAPS = { chatHeader: 4, sidebarFooter: 3, composerToolbar: 6 }
export const SLOT_ITEM_TYPES = ['page', 'tool', 'command']
export const SLOT_LIMITS = { label: 40, arguments: 2048, argumentKeys: 16 }

const PAGE_NAME = /^[a-z0-9][a-z0-9-]{0,63}$/
const ICON_NAME = /^[a-z0-9-]{1,40}$/

const FIELDS = {
  page: ['type', 'page', 'folder', 'label', 'icon'],
  tool: ['type', 'tool', 'arguments', 'label', 'icon'],
  command: ['type', 'command', 'label', 'icon'],
}

const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)
const scalar = (v) => typeof v === 'string' || typeof v === 'boolean' || (typeof v === 'number' && Number.isFinite(v))

// toolArguments says whether v may be the fixed arguments of a tool button:
// an object of at most 16 plain values, at most 2 KiB as JSON.
export function toolArguments(v) {
  if (v === undefined) return true
  if (!isObject(v)) return false
  const keys = Object.keys(v)
  if (keys.length > SLOT_LIMITS.argumentKeys || !keys.every((k) => scalar(v[k]))) return false
  return JSON.stringify(v).length <= SLOT_LIMITS.arguments
}

// slotItemOk says whether an item can show: the type, the field of its type,
// no field of another type, and values in their bounds. An item that cannot is
// left out of the bar (the server refuses such a file anyway).
export function slotItemOk(it) {
  if (!isObject(it) || !SLOT_ITEM_TYPES.includes(it.type)) return false
  if (Object.keys(it).some((k) => !FIELDS[it.type].includes(k))) return false
  if (it.label !== undefined && (typeof it.label !== 'string' || it.label.trim() === '' || it.label.length > SLOT_LIMITS.label)) return false
  if (it.icon !== undefined && !(typeof it.icon === 'string' && ICON_NAME.test(it.icon))) return false
  switch (it.type) {
    case 'page':
      return typeof it.page === 'string' && PAGE_NAME.test(it.page) &&
        (it.folder === undefined || (Number.isInteger(it.folder) && it.folder > 0))
    case 'tool':
      return typeof it.tool === 'string' && TOOL_NAME_RE.test(it.tool) && typeof it.label === 'string' && toolArguments(it.arguments)
    default:
      return commandNameOk(it.command)
  }
}

// slotKey names an item as the server does: a page by its folder and name, a
// tool by its name and arguments, a command by its name.
export function slotKey(it) {
  if (!isObject(it)) return ''
  switch (it.type) {
    case 'page': return `page:${it.folder || 0}/${it.page}`
    case 'tool': {
      const args = isObject(it.arguments) ? Object.fromEntries(Object.keys(it.arguments).sort().map((k) => [k, it.arguments[k]])) : null
      return `tool:${it.tool}:${JSON.stringify(args)}`
    }
    case 'command': return `command:${it.command}`
    default: return ''
  }
}

// readSlots reads the slots of ui.json: every known slot, as a list of the
// items that can show, at most the cap of the slot, each once.
export function readSlots(ui) {
  const raw = isObject(ui?.slots) ? ui.slots : {}
  const out = {}
  for (const name of SLOT_NAMES) {
    const seen = new Set()
    out[name] = (Array.isArray(raw[name]) ? raw[name] : []).filter((it) => {
      if (!slotItemOk(it)) return false
      const k = slotKey(it)
      if (seen.has(k)) return false
      seen.add(k)
      return true
    }).slice(0, SLOT_CAPS[name])
  }
  return out
}

// cleanSlots is what a save of the Slots tab writes: the known slots with the
// items that can show, dropping an empty slot, and keeping a slot this
// version does not know (the server refuses it, so the error names it).
export function cleanSlots(slots, extra = {}) {
  const out = {}
  for (const [k, v] of Object.entries(extra || {})) if (!SLOT_NAMES.includes(k)) out[k] = v
  for (const name of SLOT_NAMES) {
    const list = (slots?.[name] || []).map((it) => {
      if (!isObject(it) || !FIELDS[it.type]) return null
      const copy = {}
      for (const f of FIELDS[it.type]) if (it[f] !== undefined && it[f] !== '') copy[f] = it[f]
      return copy
    }).filter(slotItemOk)
    if (list.length) out[name] = list
  }
  return out
}

// mergeSlots puts an edit on top of a newer file, slot by slot and item by
// item, as the Navigation tab does: base is the file the edit started from,
// mine the edited slots, theirs the file now. An item the edit removed stays
// removed, an item the edit added is added, an item the other writer added
// stays, in the order of the edit with the other writer's new items at the end.
export function mergeSlots(base, mine, theirs) {
  const b = readSlots({ slots: base })
  const t = readSlots({ slots: theirs })
  const out = {}
  for (const name of SLOT_NAMES) {
    const baseKeys = new Set(b[name].map(slotKey))
    const mineList = mine?.[name] || []
    const mineKeys = new Set(mineList.map(slotKey))
    const theirKeys = new Set(t[name].map(slotKey))
    const removed = new Set([...baseKeys].filter((k) => !mineKeys.has(k)))
    const list = mineList.filter((it) => theirKeys.has(slotKey(it)) || !baseKeys.has(slotKey(it)))
    for (const it of t[name]) {
      const k = slotKey(it)
      if (!mineKeys.has(k) && !removed.has(k) && !baseKeys.has(k)) list.push(it)
    }
    out[name] = list.slice(0, SLOT_CAPS[name])
  }
  return out
}

// canAddTo says whether a slot has room for one more item.
export const canAddTo = (slots, name) => (slots?.[name]?.length || 0) < SLOT_CAPS[name]

// itemLabel is the text of a button: its own label, else the name of what it
// runs. A page's title comes from the page (the caller passes it).
export function itemLabel(it, pageTitle = '') {
  if (typeof it?.label === 'string' && it.label.trim()) return it.label.trim()
  if (it?.type === 'page') return pageTitle || it.page
  if (it?.type === 'tool') return it.tool
  if (it?.type === 'command') return `/${it.command}`
  return ''
}

// resolveCommand finds the command a command item names among the user's
// commands (commands.json, as readCommands in userCommands.js gives them), or
// null: an item whose command is gone shows nothing.
export function resolveCommand(it, commands) {
  if (!isObject(it) || it.type !== 'command' || typeof it.command !== 'string') return null
  return (Array.isArray(commands) ? commands : []).find((c) => isObject(c) && c.name === it.command) || null
}

// toolPrompt is the message a tool button sends to the chat after the person
// approved it: visible, a user message, and naming the tool and its arguments
// so the assistant calls that tool. The tool's own approval still applies.
export function toolPrompt(it) {
  const args = isObject(it?.arguments) && Object.keys(it.arguments).length ? ` with the arguments ${JSON.stringify(it.arguments)}` : ''
  return `Run the tool ${it.tool}${args}.`
}

// The card that opens under a compact tile: the page's widget size decides
// how tall it is. The frame keeps its document; only its box and the mode it
// is told change.
export const CARD_SIZES = ['small', 'medium', 'large']
export function cardSize(page) {
  const s = page?.widget?.size
  return CARD_SIZES.includes(s) ? s : 'medium'
}

// tileMode is the mode a frame of a slot tile is told: compact in the strip,
// and the page's widget size while its card is open.
export function tileMode(open, page) {
  return { mode: 'widget', size: open ? cardSize(page) : 'compact' }
}

// The seam of the dock: a page button of a slot, and the Open of a compact
// tile's card, open the page with the opener the chat page registers while it
// shows (usePageDock: the page goes into the dock of the open chat), and in
// the full page view anywhere else.
let opener = null
export function setPageOpener(fn) {
  opener = typeof fn === 'function' ? fn : null
  return () => { if (opener === fn) opener = null }
}
export function openSlotPage(ref, fallback) {
  if (opener && opener(ref) !== false) return 'dock'
  fallback(ref)
  return 'page'
}
