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
