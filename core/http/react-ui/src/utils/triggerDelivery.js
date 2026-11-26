// Where the runs of a trigger report, read from the `delivery` object that the
// triggers API returns. No React, so a node test drives it. The server sends
// no token and no whole chat ID; it only shortens what a chat app calls a
// chat or a channel.

// What can go wrong with a destination, by the `state` the server returns.
// Every other state (ok, or none for the Inbox) is fine.
export const DELIVERY_PROBLEMS = ['off', 'gone', 'unavailable', 'no_channel', 'blocked']

// The shape the lists draw: { kind, place, connection, service, problem, mode }.
// kind is 'inbox', 'channel' or 'chat'. place is the channel name or the short
// chat; connection is the name the owner gave the connection; service is the
// title of the app (Telegram, Slack); problem is a state of DELIVERY_PROBLEMS or ''.
export function deliveryOf(trigger) {
  const d = trigger?.delivery || {}
  const mode = d.mode && d.mode !== 'always' ? d.mode : ''
  if (d.target === 'channel') {
    return {
      kind: 'channel',
      place: d.channel_label || d.channel_ref || '',
      connection: d.connection_name || '',
      service: d.connection_kind_title || d.connection_kind || '',
      addon: d.connection_addon || d.connection_kind || '',
      connectionId: d.connection_id || '',
      problem: DELIVERY_PROBLEMS.includes(d.state) ? d.state : '',
      mode,
    }
  }
  if (d.target === 'origin') {
    return {
      kind: 'chat',
      place: d.chat_ref || '',
      thread: Boolean(d.in_thread),
      connection: d.connection_name || '',
      service: d.connection_kind_title || d.connection_kind || '',
      addon: d.connection_addon || d.connection_kind || '',
      connectionId: d.connection_id || '',
      problem: DELIVERY_PROBLEMS.includes(d.state) ? d.state : '',
      mode,
    }
  }
  return { kind: 'inbox', place: '', connection: '', service: '', addon: '', connectionId: '', problem: '', mode }
}

// ---- The delivery field of the trigger form --------------------------------
//
// The form keeps a choice { key, mode }. key is:
//   'keep'   the channel the trigger reports to now (shown only while it does);
//            the read API gives no whole channel ID, so the form names it by
//            this key and the server keeps the place
//   'inbox'  the Inbox
//   'origin' the chat of a messaging app that proposed the trigger
//   'ch:…'   a channel the owner approved: channelKey(connection, channel)
// mode is always, on_change or on_problem.

export const DELIVERY_MODES = ['always', 'on_change', 'on_problem']

// The key of an approved channel. JSON keeps IDs with any character apart.
export function channelKey(connection, channel) {
  return `ch:${JSON.stringify([connection, channel])}`
}

function parseChannelKey(key) {
  try {
    const [connection, channel] = JSON.parse(key.slice(3))
    return { connection: String(connection || ''), channel: String(channel || '') }
  } catch {
    return { connection: '', channel: '' }
  }
}

// The choice the form starts with, for a stored trigger (or null for a new one).
export function deliveryChoice(trigger) {
  const d = trigger?.delivery || {}
  const mode = DELIVERY_MODES.includes(d.mode) ? d.mode : 'always'
  if (d.target === 'channel') return { key: 'keep', mode }
  if (d.target === 'origin') return { key: 'origin', mode }
  return { key: 'inbox', mode }
}

// The options of the picker, from GET /api/chats/triggers/destinations:
//   [{ key, kind: 'inbox'|'origin'|'channel'|'keep', label?, connection?, service?, addon? }]
// Only channels that the server says can receive a report are offered; the
// channel the trigger reports to now is the 'keep' option, not a second row.
// keepable says that the trigger reports to a channel now.
export function deliveryOptions(dest, { keepable = false } = {}) {
  const out = []
  if (keepable) out.push({ key: 'keep', kind: 'keep' })
  out.push({ key: 'inbox', kind: 'inbox' })
  if (dest?.from_chat) out.push({ key: 'origin', kind: 'origin' })
  for (const c of dest?.connections || []) {
    if (!c.enabled || !c.can_receive_reports || !c.talkback_allowed) continue
    for (const ch of c.channels || []) {
      if (keepable && ch.current) continue
      out.push({
        key: channelKey(c.id, ch.id), kind: 'channel', label: ch.label || ch.id,
        connection: c.name || c.id, service: c.kind_title || c.kind || '', addon: c.addon || c.kind || '',
      })
    }
  }
  return out
}

// The connections that offer no channel, with the reason the server gives,
// so the form can say why a connection is missing from the picker.
export function unusableConnections(dest) {
  return (dest?.connections || []).filter((c) => !(c.channels || []).length && c.reason)
    .map((c) => ({ id: c.id, name: c.name || c.id, service: c.kind_title || c.kind || '', reason: c.reason }))
}

// The delivery of a create or replace request, or undefined to leave the
// trigger as it is. initial is the choice the form started with; fromChat says
// that a messaging app proposed the trigger, which keeps its connection.
export function deliveryPayload(choice, initial, { fromChat = false } = {}) {
  if (!choice) return undefined
  if (initial && choice.key === initial.key && choice.mode === initial.mode) return undefined
  const mode = DELIVERY_MODES.includes(choice.mode) ? choice.mode : 'always'
  if (choice.key === 'keep') return { target: 'channel', mode }
  if (choice.key === 'origin') return { target: 'origin', mode }
  if (choice.key.startsWith('ch:')) {
    const { connection, channel } = parseChannelKey(choice.key)
    return fromChat ? { target: 'channel', channel, mode } : { target: 'channel', connection, channel, mode }
  }
  return { target: 'inbox', mode }
}

// The words of one option of the picker; t is the agents translator.
export function deliveryOptionLabel(option, trigger, t) {
  if (!option) return ''
  if (option.kind === 'keep') {
    const d = deliveryOf(trigger)
    const app = d.connection && d.service ? `${d.connection} (${d.service})` : (d.connection || d.service)
    const place = [t('jobs.reports.channel', { channel: d.place }), app].filter(Boolean).join(' - ')
    return t('jobs.edit.delivery.keep', { place })
  }
  if (option.kind === 'inbox') return t('jobs.reports.inbox')
  if (option.kind === 'origin') return t('jobs.edit.delivery.origin')
  return t('jobs.reports.channel', { channel: option.label })
}

// The label of a mode of delivery.
export function deliveryModeLabel(mode, t) {
  return mode === 'always' ? t('jobs.edit.delivery.always') : t(`jobs.reports.mode.${mode}`)
}

