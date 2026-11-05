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
