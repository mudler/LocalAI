// What a connection will do, read from the access preview of the host. No
// React, so a node test drives it. Three jobs: the request of the preview for
// the form as it is now, the one fix button of a blocked capability, and the
// rows of the readiness list.
import { approvalPayload, channelEntries, channelOnly, listEntries, trustOf } from './connector.js'

const enc = encodeURIComponent

// ---- The preview request -------------------------------------------------

// The body of the preview call for the form as it is now. It holds only what
// the permission chain reads; there is no token. A connection that is not
// saved has the id "new" and says its kind.
export function previewBody(form, kind) {
  const body = {
    agent_id: form.agent_id || '',
    accept_any: Boolean(form.accept_any),
    allow: form.accept_any ? [] : (form.allow || []).map((x) => x.trim()).filter((x, i, a) => x && a.indexOf(x) === i),
    allow_trust: form.accept_any ? {} : Object.fromEntries((form.allow || []).map((x) => x.trim()).filter((x) => form.allow_trust?.[x]).map((x) => [x, form.allow_trust[x]])),
    caps: [...(form.caps || [])],
    grants: (form.grants || []).map((g) => ({ role: g.role, caps: [...g.caps] })),
    // Places the bot may post, and the choice for proposed triggers: the host shows them in its answer.
    channels: (form.channels || []).map((c) => ({ id: (c.id || '').trim(), label: (c.label || '').trim(), approved: Boolean(c.approved) })),
  }
  if (form.approval?.mode) body.approval_mode = form.approval.mode
  if (!form.id) body.kind = kind
  return body
}

export const previewId = (form) => form.id || 'new'

// A stable key of everything a preview depends on, to know when to ask again.
export const previewKey = (form, kind) => JSON.stringify([previewId(form), previewBody(form, kind)])

// The form fields that differ from the saved binding, as a set of names.
// With no saved binding, every field counts as changed.
const WATCHED = ['agent_id', 'accept_any', 'allow', 'allow_trust', 'caps', 'grants', 'approval', 'send_files', 'file_receive_mib', 'file_send_mib', 'notify_chat']
export function changedFields(form, saved) {
  if (!saved) return new Set(WATCHED)
  const norm = (f, k) => JSON.stringify(k === 'approval' ? approvalPayload(f.approval) : k === 'send_files' ? f.send_files !== false : (f[k] ?? ''))
  const sorted = (f, k) => (k === 'allow_trust' ? JSON.stringify(Object.entries(f[k] || {}).sort()) : k === 'allow' || k === 'caps' ? JSON.stringify([...(f[k] || [])].sort()) : norm(f, k))
  return new Set(WATCHED.filter((k) => sorted(form, k) !== sorted(saved, k)))
}

// ---- What a save changed -------------------------------------------------

// The parts of the form that a person knows by name, for the notice after a
// save. Several fields can belong to one part.
const CHANGE_PART = {
  name: 'name', agent_id: 'agent', accept_any: 'who', allow: 'who', allow_trust: 'who', caps: 'permissions', grants: 'permissions',
  approval: 'approval', send_files: 'files', file_receive_mib: 'files', file_send_mib: 'files', notify_chat: 'notify',
}

// The parts that a save changed, in a fixed order, as keys of saved.part.*.
// With no saved binding (a new connection) the list is empty.
export function changedParts(form, saved) {
  if (!saved) return []
  const fields = changedFields(form, saved)
  if ((form.name || '').trim() !== (saved.name || '')) fields.add('name')
  const parts = new Set([...fields].map((f) => CHANGE_PART[f]).filter(Boolean))
  return Object.values(CHANGE_PART).filter((p, i, all) => all.indexOf(p) === i && parts.has(p))
}

// The acts that the approval of the saved binding allowed and the form no longer does.
export function removedActs(saved, form) {
  const now = form?.approval?.acts || []
  return (saved?.approval?.acts || []).filter((n) => !now.includes(n))
}

// The triggers that a save leaves with fewer acts. A trigger made without
// asking under a connection stays within the acts the connection allows (the
// host decides this at each run), so a removed act that the trigger holds is
// dropped from its runs. Gives [{ id, name, acts }] for the connection id.
export function droppedActs(triggers, bindingId, removed) {
  if (!bindingId || !removed?.length) return []
  return (Array.isArray(triggers) ? triggers : [])
    .filter((t) => t && t.binding === bindingId)
    .map((t) => ({ id: t.id, name: t.name || t.id, acts: (t.acts || []).filter((n) => removed.includes(n)) }))
    .filter((t) => t.acts.length > 0)
}

// ---- Fix routing ---------------------------------------------------------

// What a person sees of the Tools of an agent: the editor of the agent.
const agentEditor = (id) => `/app/agents/${enc(id)}/edit`

const sel = (testid) => `[data-testid="${testid}"]`

// Where the page jumps for a field of the connection form. lists are the
// allowlists of the page (their test ids), so the jump finds the right field.
export function connectionTargets(fix, lists = [], form = {}) {
  const field = fix.field || ''
  switch (fix.target) {
    case 'caps':
      return field ? [sel(`connector-cap-switch-${field}`), sel(`connector-cap-${field}`)] : [sel('connector-matrix')]
    case 'grants':
      return [sel('connector-matrix')]
    case 'agent_id':
      return [sel('connector-agent')]
    case 'allow':
      return allowTargets(lists, form)
    case 'trust':
      return [sel('connector-trust-level'), sel('connector-who-channels'), sel('connector-who')]
    case 'token':
      return ['[data-testid="connector-form"] input[type="password"]', sel('connector-form')]
    case 'save':
      return [sel('connector-save')]
    case 'usual':
      return [sel('connector-usual'), sel('connector-matrix')]
    case 'notify':
      return [sel('connector-notify')]
    case 'files':
      return [sel('connector-files')]
    case 'approval':
      return [sel('connector-cap-switch-triggers.propose'), sel('connector-matrix')]
    default:
      return []
  }
}

// The field that adds a person. With "anyone" there is no list, so the jump
// goes to the choice of who may write.
function allowTargets(lists, form) {
  if (form.accept_any) return [sel('connector-who')]
  const first = lists[0]?.testId
  return first ? [`${sel(first)} input`, sel(first), sel('connector-who')] : [sel('connector-who')]
}

// The pairing block: its button when the connection is saved, else the block.
export const pairingTargets = () => [sel('connector-pair-start'), sel('connector-pairing')]

// What a fix does, as data the component draws:
//   { type: 'jump', selectors }  scroll to a field of this page and focus it
//   { type: 'link', to }         open another page of the app
//   { type: 'text', key }        only words (the person cannot do it)
//   { type: 'test' }             run the token test
//   null                         no fix
// ctx: { agentId, isAdmin (true, false or null when unknown), lists, form }
export function fixAction(fix, ctx = {}) {
  if (!fix || !fix.kind) return null
  switch (fix.kind) {
    case 'open_connection': {
      const selectors = connectionTargets(fix, ctx.lists, ctx.form)
      return selectors.length ? { type: 'jump', selectors } : null
    }
    case 'open_agent': {
      const id = fix.target || ctx.agentId
      return id ? { type: 'link', to: agentEditor(id) } : null
    }
    case 'open_server_setting':
      return ctx.isAdmin === true ? { type: 'link', to: '/app/settings' } : { type: 'text', key: 'askAdmin' }
    case 'pair_sender':
      return { type: 'jump', selectors: pairingTargets() }
    case 'add_person': {
      return { type: 'jump', selectors: allowTargets(ctx.lists || [], ctx.form || {}) }
    }
    case 'test_token':
      return { type: 'test' }
    case 'save':
      return { type: 'save', selectors: connectionTargets({ target: 'save' }) }
    default:
      return null
  }
}

// The button label key of a fix.
export function fixLabelKey(fix) {
  if (!fix) return ''
  if (fix.label) return fix.label
  return `fix.${fix.kind}`
}

// A title as a sentence, with the product name right.
export function sentenceCase(text) {
  const t = String(text || '').replace(/\blocalai\b/gi, 'LocalAI')
  return t.charAt(0).toUpperCase() + t.slice(1)
}

// The links still to check for a blocked row. The sender is known (it is the
// one chosen in the selector), so "sender" is never shown as unchecked.
export const remainingChecks = (thenCheck) => (thenCheck || []).filter((x) => x !== 'sender' && THEN_CHECK.includes(x))

// The links after the first problem that the check did not look at, as the
// words of the page.
export const THEN_CHECK = ['server', 'tools', 'agent', 'connection', 'sender', 'approval']

// ---- Reading the preview -------------------------------------------------

export const decisionOf = (view, subject, cap) =>
  (view?.subjects || []).find((s) => s.id === subject)?.decisions.find((d) => d.capability === cap) || null

// Whether an active pairing is on the connection.
export function hasPairing(paired, now = Date.now()) {
  return (paired || []).some((p) => !p.expires_at || new Date(p.expires_at).getTime() > now)
}

// The kinds of sender that can reach the bot with the form as it is. People on
// the list are "listed" and can pair; a channel lets in "anyone" of it, who is
// not trusted one by one. Roles come from the platform and apply to whoever
// gets in.
export function reachable(form, lists, view) {
  const roles = (view?.subjects || []).filter((s) => s.id.startsWith('role:')).map((s) => s.id)
  if (form.accept_any) return ['anyone', 'paired', ...roles]
  const multi = Array.isArray(lists) && lists.length > 1
  const allow = form.allow || []
  const people = multi ? listEntries(allow, lists, 0).length > 0 : allow.length > 0
  const channels = multi && allow.length > listEntries(allow, lists, 0).length
  const out = []
  if (people) out.push('listed', 'paired')
  if (channels) out.push('anyone')
  if ((form.allow || []).some((x) => trustOf(form, x) === 'act')) out.push('channel_acts')
  if (people || channels) out.push(...roles)
  return out
}

// What blocks a ticked capability for everyone who can reach the bot, or null
// when somebody can use it. The answer is a decision in the shape of the
// host's (message, fix, code), with a stand-in for what only the form knows:
// nobody is let in, or a pairing is missing.
export function unusable(cap, view, form, lists, paired, now) {
  const subjects = reachable(form, lists, view)
  if (subjects.length === 0) {
    return { capability: cap, allowed: false, code: 'nobody_let_in', blocked_by: 'sender', fix: { kind: 'add_person', target: 'allow' } }
  }
  const decisions = subjects.map((id) => decisionOf(view, id, cap)).filter(Boolean)
  if (decisions.length === 0) return null
  const usable = decisions.filter((d) => d.allowed)
  if (usable.length > 0) {
    // A capability that only a paired sender holds is dead until somebody is paired.
    if (usable.every((d) => d.via === 'paired' || d.via === 'agent-connection') && !hasPairing(paired, now)) {
      return { capability: cap, allowed: false, code: 'nobody_paired', blocked_by: 'sender', fix: { kind: 'pair_sender', target: 'pairing' } }
    }
    return null
  }
  return decisions.find((d) => d.blocked_by !== 'sender') || decisions[0]
}

// ---- The readiness rows --------------------------------------------------

// A row that needs the owner: shown even when the list is collapsed.
export const isProblem = (state) => state === 'attention' || state === 'broken' || state === 'after_save' || state === 'unknown'

const jump = (target, field, label) => ({ kind: 'open_connection', target, field, label })

// The rows of the readiness list for the form as it is now.
//   form, saved (the form of the saved binding, or null), kindInfo,
//   capabilities, agents, lists, tokenTyped, token ({ ok, reason } | null),
//   preview ({ status: 'loading'|'ready'|'error', view }), paired, agentOptional, toolOnly,
//   now
// Every row is { id, state, key, params, fix } with the sentence as a key of
// the page's words. A row that works only after the form is saved says so.
export function readinessRows(ctx) {
  const { form, saved, kindInfo, agents = [], lists = [], tokenTyped, token, preview = {}, paired, agentOptional = false, toolOnly = false, now = Date.now(), capabilities = [], isChannel = () => false } = ctx
  const changed = changedFields(form, saved)
  const caps = form.caps || []
  const on = (n) => caps.includes(n)
  const view = preview.status === 'ready' ? preview.view : null
  const pending = preview.status === 'loading' || preview.status === undefined ? 'checking' : preview.status === 'error' ? 'unknown' : ''
  const titleOf = (n) => sentenceCase(capabilities.find((c) => c.name === n)?.title || n)
  const afterSave = (row, ...fields) => (row.state === 'ok' && fields.some((f) => changed.has(f)) ? { ...row, state: 'after_save', fix: { kind: 'save', label: 'fix.save' } } : row)
  const rows = []

  // Token
  if (!form.id) {
    rows.push(tokenTyped
      ? { id: 'token', state: 'after_save', key: 'ready.token.newTyped', fix: { kind: 'save', label: 'fix.save' } }
      : { id: 'token', state: 'attention', key: 'ready.token.missing', fix: jump('token', '', 'fix.token') })
  } else if (tokenTyped) {
    rows.push({ id: 'token', state: 'after_save', key: 'ready.token.typed', fix: { kind: 'save', label: 'fix.save' } })
  } else if (token && token.ok === false) {
    rows.push({ id: 'token', state: 'broken', key: 'ready.token.failed', params: { reason: token.reason || '' }, fix: jump('token', '', 'fix.tokenNew') })
  } else if (token && token.ok) {
    rows.push({ id: 'token', state: 'ok', key: 'ready.token.checked', params: { bot: token.bot || '' } })
  } else {
    rows.push({ id: 'token', state: 'ok', key: 'ready.token.saved' })
  }

  // Agent. A tool-only kind has none: agents choose the connection, not the other way round.
  const agent = agents.find((a) => a.id === form.agent_id)
  if (toolOnly) {
    // no row
  } else if (!form.agent_id) {
    rows.push(agentOptional
      ? { id: 'agent', state: 'off', key: 'ready.agent.credentialsOnly' }
      : { id: 'agent', state: 'attention', key: 'ready.agent.none', fix: jump('agent_id', '', 'fix.agent') })
  } else if (agents.length > 0 && !agent) {
    rows.push({ id: 'agent', state: 'attention', key: 'ready.agent.gone', fix: jump('agent_id', '', 'fix.agent') })
  } else if (agent && !agent.model) {
    rows.push({ id: 'agent', state: 'broken', key: 'ready.agent.noModel', params: { name: agent.name }, fix: { kind: 'open_agent', target: agent.id } })
  } else {
    rows.push(afterSave({ id: 'agent', state: 'ok', key: agent ? 'ready.agent.ok' : 'ready.agent.okUnknown', params: { name: agent?.name || '', model: agent?.model || '' } }, 'agent_id'))
  }

  // Who may use it
  const multi = lists.length > 1
  const entries = form.allow || []
  const people = multi ? listEntries(entries, lists, 0).length : entries.length
  if (form.accept_any) {
    rows.push(afterSave({ id: 'who', state: 'ok', key: 'ready.who.anyone' }, 'accept_any', 'allow'))
  } else if (entries.length === 0) {
    rows.push({ id: 'who', state: 'attention', key: 'ready.who.nobody', fix: { kind: 'add_person', target: 'allow' } })
  } else if (channelOnly(form, lists)) {
    rows.push({ id: 'who', state: 'attention', key: 'ready.who.channelsOnly', fix: { kind: 'add_person', target: 'allow' } })
  } else {
    rows.push(afterSave({ id: 'who', state: 'ok', key: multi && entries.length > people ? 'ready.who.mixed' : 'ready.who.listed', params: { count: entries.length } }, 'accept_any', 'allow'))
  }

  // The channels where the bot listens: one sentence on what the people in them
  // may do, and one on where it may post, which is another list.
  const listening = form.accept_any ? [] : channelEntries(form, isChannel)
  const acting = listening.filter((x) => trustOf(form, x) === 'act')
  const answering = listening.filter((x) => trustOf(form, x) !== 'act')
  if (answering.length > 0) {
    rows.push({ id: 'channelTrust', state: 'attention', key: 'ready.channelTrust.answer', params: { channels: answering.join(', ') }, fix: jump('trust', '', 'fix.trust') })
  } else if (acting.length > 0 && caps.length === 0) {
    rows.push({ id: 'channelTrust', state: 'attention', key: 'ready.channelTrust.noCaps', fix: jump('usual', '', 'fix.usual') })
  } else if (acting.length > 0) {
    rows.push(afterSave({ id: 'channelTrust', state: 'ok', key: 'ready.channelTrust.act', params: { channels: acting.join(', ') } }, 'allow', 'allow_trust', 'caps'))
  }
  if (on('talkback.channels') && (form.channels || []).length > 0) {
    rows.push({ id: 'postTo', state: 'ok', key: 'ready.postTo', params: { count: form.channels.length } })
  }

  // Permissions (all ticked ones except the two with a row of their own)
  const others = caps.filter((n) => n !== 'triggers.propose' && n !== 'files.upload')
  if (caps.length === 0) {
    rows.push({ id: 'permissions', state: saved ? 'attention' : 'off', key: saved ? 'ready.perm.noneSaved' : 'ready.perm.none', fix: jump('usual', '', 'fix.usual') })
  } else if (others.length === 0) {
    rows.push(afterSave({ id: 'permissions', state: 'ok', key: 'ready.perm.onlyOwnRows' }, 'caps'))
  } else if (pending) {
    rows.push({ id: 'permissions', state: pending, key: pending === 'checking' ? 'ready.checking' : 'ready.unchecked' })
  } else {
    const dead = others.map((n) => unusable(n, view, form, lists, paired, now)).filter(Boolean)
    if (dead.length > 0) {
      const first = dead[0]
      rows.push({
        id: 'permissions', state: 'attention', key: 'ready.perm.dead',
        params: { count: dead.length, names: dead.map((d) => titleOf(d.capability)).join(', '), message: messageOf(first), messageKey: messageKey(first) },
        fix: first.fix,
      })
    } else {
      rows.push(afterSave({ id: 'permissions', state: 'ok', key: 'ready.perm.ok', params: { count: others.length } }, 'caps', 'grants', 'allow', 'accept_any'))
    }
  }

  // Tools of the add-ons the agent uses that a permission of this connection
  // decides: without the tick the agent has them in no messaging chat, and
  // nothing else on the page says so.
  for (const hold of view?.addon_tools || []) {
    const names = (hold.tools || []).join(', ')
    const params = { addon: hold.addon, tools: names, permission: titleOf(hold.capability) }
    const id = `addons:${hold.addon}`
    if (!on(hold.capability)) {
      // Not ticked. A paired person still has the write tools when the agent is
      // connected to a connection of the add-on that is not in write mode off:
      // the connection already says what the owner wants. The tick is for the
      // people who are not paired.
      const conns = hold.connections || []
      const live = conns.find((c) => c.write_mode && c.write_mode !== 'off')
      if ((hold.without_tick || []).includes('paired') && live) {
        const p = { ...params, connection: live.name, modeKey: `ready.addons.mode.${live.write_mode}` }
        if (!hasPairing(paired, now)) {
          const d = { capability: hold.capability, code: 'nobody_paired', fix: { kind: 'pair_sender', target: 'pairing' } }
          rows.push({ id, labelKey: 'ready.row.addons', state: 'attention', key: 'ready.addons.blocked', params: { ...params, message: messageOf(d), messageKey: messageKey(d) }, fix: d.fix })
        } else {
          rows.push(afterSave({ id, labelKey: 'ready.row.addons', state: 'ok', key: 'ready.addons.paired', params: p }, 'caps', 'agent_id'))
          // A connection that asks first: the bot writes the proposal in the
          // chat and a paired person answers it there.
          if (conns.some((c) => c.write_mode === 'propose') && on('proposals.confirm')) {
            rows.push(afterSave({ id: `${id}:confirm`, labelKey: 'ready.row.addons', state: 'ok', key: 'ready.addons.confirmChat', params: { addon: hold.addon } }, 'caps', 'agent_id'))
          }
        }
      } else if (hold.tick_removed) {
        rows.push({ id, labelKey: 'ready.row.addons', state: 'attention', key: 'ready.addons.removed', params, fix: jump('caps', hold.capability, 'fix.turnOn') })
      } else if (conns.length > 0 && !live) {
        rows.push({ id, labelKey: 'ready.row.addons', state: 'attention', key: 'ready.addons.modeOff', params: { ...params, connection: conns.map((c) => c.name).join(', ') } })
      } else {
        rows.push({ id, labelKey: 'ready.row.addons', state: 'attention', key: 'ready.addons.off', params, fix: jump('caps', hold.capability, 'fix.turnOn') })
      }
      continue
    }
    const d = unusable(hold.capability, view, form, lists, paired, now)
    if (d) rows.push({ id, labelKey: 'ready.row.addons', state: 'attention', key: 'ready.addons.blocked', params: { ...params, message: messageOf(d), messageKey: messageKey(d) }, fix: d.fix })
    else {
      rows.push(afterSave({ id, labelKey: 'ready.row.addons', state: 'ok', key: 'ready.addons.ok', params }, 'caps', 'grants', 'allow', 'accept_any', 'agent_id'))
      if ((hold.connections || []).some((c) => c.write_mode === 'propose') && on('proposals.confirm') && hasPairing(paired, now)) {
        rows.push(afterSave({ id: `${id}:confirm`, labelKey: 'ready.row.addons', state: 'ok', key: 'ready.addons.confirmChat', params: { addon: hold.addon } }, 'caps', 'agent_id'))
      }
    }
  }

  // Triggers
  if (!on('triggers.propose')) {
    rows.push({ id: 'triggers', state: 'off', key: 'ready.triggers.off', fix: jump('caps', 'triggers.propose', 'fix.turnOn') })
  } else if (pending) {
    rows.push({ id: 'triggers', state: pending, key: pending === 'checking' ? 'ready.checking' : 'ready.unchecked' })
  } else {
    const d = unusable('triggers.propose', view, form, lists, paired, now)
    if (d) {
      rows.push({ id: 'triggers', state: 'attention', key: 'ready.triggers.blocked', params: { message: messageOf(d), messageKey: messageKey(d) }, fix: d.fix })
    } else {
      const mode = form.approval?.mode || 'ask'
      rows.push(afterSave({ id: 'triggers', state: 'ok', key: `ready.triggers.ok.${mode}` }, 'caps', 'approval', 'allow', 'accept_any', 'agent_id'))
    }
  }

  // Files
  const filesOff = kindInfo?.files && !kindInfo.files.receive?.allowed_bytes && !kindInfo.files.send?.allowed_bytes
  if (!filesOff) {
    if (!on('files.upload')) {
      rows.push({ id: 'files', state: 'off', key: form.send_files === false ? 'ready.files.offBoth' : 'ready.files.offReceive', fix: jump('caps', 'files.upload', 'fix.turnOn') })
    } else if (pending) {
      rows.push({ id: 'files', state: pending, key: pending === 'checking' ? 'ready.checking' : 'ready.unchecked' })
    } else {
      const d = unusable('files.upload', view, form, lists, paired, now)
      if (d) rows.push({ id: 'files', state: 'attention', key: 'ready.files.blocked', params: { message: messageOf(d), messageKey: messageKey(d) }, fix: d.fix })
      else rows.push(afterSave({ id: 'files', state: 'ok', key: form.send_files === false ? 'ready.files.okReceive' : 'ready.files.ok' }, 'caps', 'send_files', 'allow', 'accept_any', 'agent_id'))
    }
  }

  // Notifications
  const notify = (form.notify_chat || '').trim()
  rows.push(notify
    ? afterSave({ id: 'notify', state: 'ok', key: 'ready.notify.ok', params: { chat: notify } }, 'notify_chat')
    : { id: 'notify', state: 'off', key: 'ready.notify.off', fix: jump('notify', '', 'fix.turnOn') })
  return rows
}

// The sentence for a blocker. The host sends one; the two cases that only the
// form knows are named by a code.
export function messageOf(d) {
  if (d.message) return d.message
  if (d.code === 'nobody_let_in') return 'Nobody is let in yet.'
  if (d.code === 'nobody_paired') return 'This needs a verified person, and nobody is verified yet.'
  return d.reason || ''
}

// The key of the translated sentence for a blocker that only the form knows
// (the host's own message is never replaced), or '' when there is none.
export function messageKey(d) {
  if (d.message) return ''
  if (d.code === 'nobody_let_in') return 'ready.blocker.nobodyLetIn'
  if (d.code === 'nobody_paired') return 'ready.blocker.nobodyPaired'
  return ''
}

// The rows of the status line: the connection itself (token, agent) and who is
// let in. The other rows say what a kind of sender may do, which the matrix
// shows cell by cell.
export const STATUS_ROW_IDS = ['token', 'agent', 'who']
export const statusRows = (rows) => rows.filter((r) => STATUS_ROW_IDS.includes(r.id))

// The rows to show, and how many are hidden, in collapsed mode: only the
// rows that need the owner.
export function visibleRows(rows, expanded) {
  if (expanded) return { shown: rows, hidden: 0 }
  const shown = rows.filter((r) => isProblem(r.state))
  return { shown, hidden: rows.length - shown.length }
}

// The rows of a connection that is not saved yet. "Keep these changes" has no
// meaning there, so a row loses its save fix. A row that only waits for the
// save is not a thing to set, so it is not a problem here.
export const forNewConnection = (rows) => rows.map((r) => (r.fix?.kind === 'save' ? { ...r, fix: undefined } : r))
export const isThingToSet = (state) => isProblem(state) && state !== 'after_save'

// How many things a new connection still needs from the person.
export const thingsToSet = (rows) => rows.filter((r) => isThingToSet(r.state)).length

// OUTCOME_TONES maps how an add-on tool call ended to the look of its badge:
// a held call waits for a person, a refused or failed one needs a fix.
const OUTCOME_TONES = { ok: 'done', held: 'skipped', denied: 'failed', error: 'failed', confirmed: 'done', cancelled: 'skipped', expired: 'skipped' }

// toolOutcomeRows lists the latest add-on tool calls of a binding for the access
// view, newest first and at most `limit`. A row names the tool and says how the
// call ended and why; the host sends names and codes, never arguments or results.
export function toolOutcomeRows(view, limit = 20) {
  return (view?.tool_outcomes || []).slice(0, limit).map((e, i) => ({
    key: `${e.at}-${e.tool}-${i}`,
    tool: e.addon ? `${e.addon} / ${e.tool}` : e.tool,
    outcome: Object.hasOwn(OUTCOME_TONES, e.outcome) ? e.outcome : 'error',
    tone: OUTCOME_TONES[e.outcome] || 'failed',
    at: e.at,
    code: e.code || '',
    message: e.message || '',
    status: e.status || 0,
    senders: e.senders || [],
    asker: e.asker || '',
    senderName: e.sender_name || '',
  }))
}
