// The earlier turns of the conversation the user is looking at, in the shape
// POST /api/agents/:name/chat accepts as `history`. Each conversation in the
// agent chat (New Chat, switching, Clear) sends only its own visible turns, so
// conversations never see each other's messages and Clear starts fresh.
export function chatHistoryFor(messages) {
  const history = []
  for (const m of messages || []) {
    const role = m?.sender === 'user' ? 'user' : m?.sender === 'agent' ? 'assistant' : null
    if (!role) continue
    const content = typeof m.content === 'string' ? m.content : ''
    if (!content.trim()) continue
    history.push({ role, content })
  }
  return history
}
