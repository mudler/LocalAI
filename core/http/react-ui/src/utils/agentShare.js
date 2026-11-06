// SPDX-License-Identifier: MIT
// Sharing a saved agent (share kind "agent"): who may share it, the rows the
// Share dialog shows for what the agent uses, how the owner's choice for each
// of them becomes a grant on that thing, and the summary of what an agent
// needs. The server decides every permission; this only shapes calls.
//
// No React and no network: the node unit tests drive it directly.

export const AGENT_KIND = 'agent'
// The roles an agent grants, in the order the dialog offers them.
export const AGENT_ROLES = Object.freeze(['use', 'viewer'])

const roleOf = (a) => a?.access?.role || ''

// canShareAgent: the caller owns the agent and the server shares agents.
export function canShareAgent(agent) {
  return roleOf(agent) === 'owner' && agent.access.sharing === true
}

// isSharedWithMe: the agent is another person's, shared with the caller.
export function isSharedWithMe(agent) {
  const r = roleOf(agent)
  return r !== '' && r !== 'owner'
}

// agentShareEntry is the entry SharedByBadge draws for a row, or null when
// there is nothing to say (a server that shares nothing, or an agent that
// was never shared on one that shares).
export function agentShareEntry(agent) {
  const a = agent?.access
  if (!a) return null
  if (a.role === 'owner') {
    if (!a.sharing && !(a.sharedWith > 0)) return null
    return { role: 'owner', sharedWith: Number(a.sharedWith) || 0 }
  }
  return { role: a.role, owner: a.owner || '', everyone: a.everyone === true }
}

// The row ID of a reference in the dialog. An add-on resource is named by
// its add-on too: two add-ons may use the same resource ID shape.
export function referenceRowId(r) {
  return r.kind === 'addon-resource' ? `addon-resource:${r.addon_id || ''}:${r.id}` : `${r.kind}:${r.id}`
}

const KINDS = new Set(['addon', 'collection', 'skill', 'folder', 'connection', 'addon-resource', 'mcp', 'builtin'])

// grantRole is the role a choice grants on a referenced thing, as the server
// named it from the roles the thing's kind declares, or '' when the server
// offered no such choice. The client keeps no table of its own: a kind that
// grants viewer and editor (a collection) and one that grants use (a
// connection) answer differently, and only the server knows.
function grantRole(share, choice) {
  if (!share || (choice !== 'view' && choice !== 'use') || !(share.options || []).includes(choice)) return ''
  const role = share.roles?.[choice]
  return typeof role === 'string' ? role : ''
}

// referenceRows turns GET /api/chats/agents/{id}/references into the
// `references` of the Share dialog. A thing that can be shared asks for one of
// the options the server offers for it, or none; any other thing is a
// read-only row that says the person needs access of their own.
export function referenceRows(references, t) {
  return (Array.isArray(references) ? references : [])
    .filter(r => r && typeof r.id === 'string' && r.id !== '' && KINDS.has(r.kind))
    .map(r => {
      const row = { id: referenceRowId(r), label: r.label || r.id, kind: t(`agents:share.kind.${r.kind}`) }
      const opts = (r.share?.options || []).filter(o => grantRole(r.share, o) !== '')
      if (opts.length > 0) return { ...row, options: [...opts, 'none'] }
      return { ...row, readOnly: true, note: t('agents:share.ownAccess') }
    })
}

// applyReferenceChoices shares each referenced thing the owner chose with the
// same principal, with the role the server named for the choice: collections and connections through the sharing API,
// add-on resources through the add-on routes (addonGrant). An add-on resource
// cannot be granted to every account, so it is skipped for @everyone. It
// returns what was applied, what failed and what was skipped, by label.
export async function applyReferenceChoices({ principal, references, choices, sharesApi, addonGrant }) {
  const out = { applied: 0, failed: [], skipped: [] }
  for (const r of Array.isArray(references) ? references : []) {
    const role = grantRole(r?.share, choices?.[referenceRowId(r)])
    if (!role) continue
    try {
      if (r.share.kind === 'addon') {
        if (principal === '@everyone') { out.skipped.push(r.label); continue }
        await addonGrant(r.addon_id, r.share.resource_id, principal, role)
      } else {
        await sharesApi.setGrant(r.share.kind, r.share.resource_id, principal, role)
      }
      out.applied++
    } catch {
      out.failed.push(r.label)
    }
  }
  return out
}

const STATES = new Set(['ok', 'missing', 'unavailable', 'ask'])

// needsSummary counts the needs of an agent for the caller and lists, in the
// server's order, the ones the caller lacks (missing, unavailable) and the
// ones the agent asks for that a chat of the caller does not start with (ask:
// a network or a group of built-in tools the caller may turn on).
export function needsSummary(needs) {
  const list = (Array.isArray(needs) ? needs : []).filter(n => n && STATES.has(n.state))
  return {
    total: list.length,
    ok: list.filter(n => n.state === 'ok').length,
    lacking: list.filter(n => n.state === 'missing' || n.state === 'unavailable'),
    asks: list.filter(n => n.state === 'ask'),
    all: list,
  }
}
