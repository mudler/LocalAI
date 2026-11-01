// The address of the chat list. With kind "chat" the server leaves sub-agent
// runs out and counts only chats against the limit, so a burst of newer runs
// cannot push a person's chats off the page.
export function chatsListPath(base, limit, kind) {
  const sp = new URLSearchParams()
  if (limit) sp.set('limit', String(limit))
  if (kind) sp.set('kind', kind)
  const s = sp.toString()
  return s ? `${base}?${s}` : base
}
