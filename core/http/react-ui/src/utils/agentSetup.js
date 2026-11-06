// The "Finish setup" card of an agent page: which apps the person wanted, which
// of them the server already has, and what the card shows. No React, so a node
// test drives it. The browser remembers only the wish and the dismissal; a row
// is done when the server says so.
import { bindingsWithKind } from './chatConnectors.js'
import { catalogAddress, kindSetupAddress } from './agentWizard.js'

export const SETUP_PREFIX = 'localai:agent-setup:'
const list = (x) => (Array.isArray(x) ? x : [])

const keyOf = (agentId) => `${SETUP_PREFIX}${agentId}`

// { wanted: [item], dismissed } for an agent. Storage may be missing or full.
export function loadSetup(agentId, storage) {
  try {
    const raw = (storage || globalThis.localStorage).getItem(keyOf(agentId))
    const v = raw ? JSON.parse(raw) : null
    return { wanted: list(v?.wanted).filter((w) => w && typeof w.key === 'string'), dismissed: v?.dismissed === true }
  } catch {
    return { wanted: [], dismissed: false }
  }
}

export function saveSetup(agentId, state, storage) {
  try {
    ;(storage || globalThis.localStorage).setItem(keyOf(agentId), JSON.stringify({ wanted: list(state.wanted), dismissed: state.dismissed === true }))
    return true
  } catch {
    return false
  }
}

// Only what is needed to draw a row later: the wish, not the answer.
export const wishOf = (item) => ({
  key: item.key, type: item.type, addon: item.addon, kind: item.kind, page: item.page,
  title: item.title, setup: item.setup, credentialed: item.credentialed,
})

// The rows of the card. `server` holds what the server says now:
//   agent      the saved agent (addons, connections)
//   kinds      the connector kinds of running add-ons
//   bindings   all connector bindings of the person
//   installed  ids of installed add-ons, or null when unknown
// A row is done when the server has the connection (chat app), the account or
// the switch (tool add-on). The title and the setup text follow the live kind.
export function setupRows(wanted, server) {
  const agent = server.agent || {}
  const kinds = list(server.kinds)
  const mine = bindingsWithKind(agent.id, kinds, list(server.bindings)).map((x) => x.c)
  return list(wanted).map((w) => {
    const live = kinds.find((k) => k.kind === w.kind && k.addon === w.addon)
    const isKind = w.type === 'kind' || (w.type === 'catalog' && kinds.some((k) => k.addon === w.addon && !(k.agent_optional ?? k.agentOptional)))
    const liveKind = isKind ? (live || kinds.find((k) => k.addon === w.addon)) : null
    const installed = w.type !== 'catalog' || Boolean(liveKind) || (server.installed ? server.installed.includes(w.addon) : false)
    const title = liveKind ? (liveKind.localized_title || liveKind.title || w.title) : w.title
    const base = { key: w.key, addon: w.addon, title, setup: liveKind?.setup || w.setup || '' }
    if (!installed) {
      return { ...base, kind: 'install', done: false, href: catalogAddress(w.addon), installed: false }
    }
    if (isKind) {
      const kindId = liveKind?.kind || w.kind
      const done = mine.some((c) => c.kind === kindId && (!c.addon || c.addon === w.addon))
      const item = { addon: w.addon, page: liveKind?.page || w.page }
      return { ...base, kind: 'chat', done, installed: true, href: kindSetupAddress(item, agent.id) }
    }
    const accounts = list(agent.connections?.[w.addon])
    const on = list(agent.addons).includes(w.addon)
    const done = w.credentialed ? accounts.length > 0 && on : on
    return {
      ...base, kind: w.credentialed ? 'account' : 'switch', done, installed: true,
      href: `/app/agents/${encodeURIComponent(agent.id)}/edit#aed-sec-capabilities`,
    }
  })
}

export function setupProgress(rows) {
  const done = rows.filter((r) => r.done).length
  return { done, total: rows.length, complete: rows.length > 0 && done === rows.length }
}

// The card is shown while something is left and the person has not dismissed it.
export function setupVisible(state, rows) {
  return rows.length > 0 && !state.dismissed && !setupProgress(rows).complete
}
