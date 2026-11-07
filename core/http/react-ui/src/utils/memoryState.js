// Whether memory works in a chat, for the screen. The server says it in the
// memory object of the tools list ({ on, code, blocked_by, fix }); the memory
// group of the list is the chat's own switch, which a draft changes before the
// server hears of it. The narrowest level that is off wins, as on the server:
// this chat, then the agent, then the person's switch, then the server.

export const MEMORY_GROUP = 'memory'

// null while memory works, else { kind, by } with kind one of chat, agent,
// user, files or provider (the add-on that serves memory is not available).
export function memoryOff(memory, group) {
  if (group?.off) return { kind: 'chat', by: 'chat' }
  if (!memory || memory.on !== false) return null
  const kind = { memory_off_chat: 'chat', memory_off_agent: 'agent', memory_off_user: 'user', memory_off_files: 'files', memory_provider_unavailable: 'provider' }[memory.code] || 'user'
  return { kind, by: memory.blocked_by || kind }
}

// Where to turn memory on again: a link for the levels the person can reach
// from the page that shows the line, or none (the chat's own switch is in the
// Tools menu right there).
export function memoryFix(off) {
  if (!off) return null
  if (off.kind === 'user' || off.kind === 'provider') return { to: '/app/account?tab=memory', key: 'fixUser' }
  return null
}
