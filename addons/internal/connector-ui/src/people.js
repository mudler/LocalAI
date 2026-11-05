// Names beside IDs. The host keeps the display name and the chat of the people
// who wrote to the bot (the sender log), and the access log carries them too.
// A page that shows an ID asks here for the name and falls back to the ID when
// it has none: nothing is lost and nothing is made up.
// The directory of a sender log. An entry whose name is only its ID adds nothing.
export function peopleFrom(senders) {
  const m = new Map()
  for (const s of senders || []) {
    if (!s || !s.id) continue
    m.set(s.id, { name: s.name && s.name !== s.id ? s.name : '', chat: s.chat || '', chatName: s.chat_name || '' })
  }
  return m
}

// The name to show beside an ID: the one the event carried, else the one of the
// directory, else none.
export function nameOf(people, id, own = '') {
  const n = own || people?.get(id)?.name || ''
  return n && n !== id ? n : ''
}

// "Ada · U001", or "U001" when no name is known.
export function personText(people, id, own = '') {
  const n = nameOf(people, id, own)
  return n ? `${n} · ${id}` : id
}

// "ops (C0123)" when the name of the chat is known, else the ID.
export function chatText(chat, chatName) {
  if (!chat) return chatName || ''
  return chatName && chatName !== chat ? `${chatName} (${chat})` : chat
}

// The names of the chats that people wrote from, by chat ID, from a sender log.
export function chatNamesFrom(senders) {
  const m = new Map()
  for (const s of senders || []) if (s?.chat && s.chat_name && s.chat_name !== s.chat) m.set(s.chat, s.chat_name)
  return m
}

// The people of a sender log who are not on the list yet, newest first, at most max.
export function recentPeople(senders, listed, max = 8) {
  const on = new Set(listed || [])
  return [...(senders || [])]
    .filter((s) => s?.id && !on.has(s.id))
    .sort((a, b) => String(b.last || '').localeCompare(String(a.last || '')))
    .slice(0, max)
    .map((s) => ({ id: s.id, name: s.name && s.name !== s.id ? s.name : '' }))
}

// The people and the channels the bot already knows, to choose where it sends
// reminders and run results: the people of the allowlist and of the sender
// log, the channels of the allowlist and the places it may post. A chat in the
// sender log counts as a channel when it is not a person. Each has an ID and,
// when known, a name. A person is { id, name }, a channel { id, name }.
export function knownTargets({ form, senders, isChannel = () => false }) {
  const people = new Map()
  const channels = new Map()
  const dir = peopleFrom(senders)
  const chatNames = chatNamesFrom(senders)
  for (const x of form?.allow || []) {
    if (isChannel(x)) channels.set(x, chatNames.get(x) || '')
    else people.set(x, dir.get(x)?.name || '')
  }
  for (const s of senders || []) {
    if (s?.id && !people.has(s.id) && !channels.has(s.id)) people.set(s.id, dir.get(s.id)?.name || '')
  }
  for (const [id, name] of chatNames) if (!people.has(id) && !channels.has(id)) channels.set(id, name)
  for (const c of form?.channels || []) {
    const id = (c.id || '').trim()
    if (id && !channels.has(id) && !people.has(id)) channels.set(id, (c.label || '').trim() || chatNames.get(id) || '')
  }
  const list = (m) => [...m].map(([id, name]) => ({ id, name: name && name !== id ? name : '' }))
  return { people: list(people), channels: list(channels) }
}
