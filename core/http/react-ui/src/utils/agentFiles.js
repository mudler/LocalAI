import { chatAgentsApi } from './api'
import { importAgent } from './agentConfigTools'
import { downloadJSON } from './download'

// Agent files go through the server: GET /api/chats/agents/{id}/export writes
// the file and POST /api/chats/agents/import reads it, always as a new agent,
// and says in warnings what it had to change. An export of the old agents
// (system_prompt, selected_skills, ...) is turned into a definition first, and
// what it holds that a saved agent has no place for is named.

export async function downloadAgentFile(agent, addToast, t) {
  try {
    const file = await chatAgentsApi.exportFile(agent.id)
    downloadJSON(`${agent.name}.json`, file)
    addToast(t('toasts.exported', { name: agent.name }), 'success')
  } catch (err) {
    addToast(t('toasts.exportFailed', { message: err.message }), 'error')
  }
}

// The body to send for a parsed file, and the old fields it drops.
export function agentImportBody(data) {
  if (data && typeof data === 'object' && !Array.isArray(data) && (data.agent || !importAgent(data)?.legacy)) {
    return { body: data, dropped: [] }
  }
  const read = importAgent(data)
  if (!read) return null
  return { body: read.agent, dropped: read.dropped }
}

// Imports a file: { agent, notes } where notes are the server's warnings and
// the old fields that were not imported.
export async function importAgentFile(text) {
  const parsed = agentImportBody(JSON.parse(text))
  if (!parsed) throw new Error('not an agent file')
  const res = await chatAgentsApi.importFile(parsed.body)
  return { agent: res.agent, warnings: res.warnings || [], dropped: parsed.dropped }
}
