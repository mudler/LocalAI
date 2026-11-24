// The address of the chat list. With kind "chat" the server leaves sub-agent
// runs out and counts only chats against the limit, so a burst of newer runs
// cannot push a person's chats off the page.
//
// With an origin the server returns only the chats of that origin and counts
// the limit against them: person (a person started it), automated (anything
// else) or one origin kind such as trigger or connector.
export function chatsListPath(base, limit, kind, origin) {
  const sp = new URLSearchParams()
  if (limit) sp.set('limit', String(limit))
  if (kind) sp.set('kind', kind)
  if (origin) sp.set('origin', origin)
  const s = sp.toString()
  return s ? `${base}?${s}` : base
}
