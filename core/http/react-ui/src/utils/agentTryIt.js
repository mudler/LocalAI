// "Try it" on the agent form: a test chat that uses the settings now in the
// form, saved or not. No endpoint runs a draft agent, so the form builds a
// plain chat from what a chat can be started with: the model, the tool
// switches (add-ons, connectors, built-in groups, skills) and the agent's
// instructions as the chat's system prompt. The chat is deleted when the
// panel closes. No React, so a node test drives it.
import { draftSwitches } from './chatDraftSwitches.js'
import { connectorRef } from './chatMCP.js'

const list = (x) => (Array.isArray(x) ? x : [])

// The create request of the test chat, or null when there is no model to run.
//   form    the agent form
//   tools   the rows of useAgentTools
//   models  the chat models the server lists
export function tryRequest({ form, tools, models }) {
  const model = (form.model || '').trim() || list(models)[0]?.id || ''
  if (!model) return null
  const live = (x) => !x.gone
  const switches = draftSwitches({
    addons: list(tools.addons).filter(live),
    mcpOff: list(tools.connectors).filter((c) => live(c) && c.off).map((c) => connectorRef(c.id)),
    builtins: list(tools.builtins),
    skills: list(tools.skills).filter(live),
  })
  return {
    model,
    usedDefaultModel: !(form.model || '').trim(),
    switches,
    // Added to the chat's instructions, as the agent's own would be.
    settings: { system_prompt: form.instructions || '' },
  }
}

// What the test chat does not use from the form, as locale keys, so the panel
// says it. The permissions (grants), the memory choice, the knowledge
// collections and the shared folders belong to a saved agent.
export function notApplied(form) {
  const out = []
  if (list(form.builtins).length) out.push('grants')
  if (form.memory) out.push('memory')
  if (list(form.collections).length) out.push('knowledge')
  if (list(form.folders).length && !list(form.builtins_off).includes('files')) out.push('folders')
  return out
}

// The lines of a transcript worth showing: what was said, and that a tool ran.
export function tryLines(messages) {
  return list(messages)
    .filter((m) => (m.role === 'user' || m.role === 'assistant') ? !!(m.text || '').trim() : m.role === 'tool')
    .map((m) => ({ seq: m.seq, role: m.role, text: m.role === 'tool' ? String(m.text || '').split('\n')[0] : m.text }))
}
