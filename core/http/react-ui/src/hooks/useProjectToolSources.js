import { useEffect, useState } from 'react'
import { agentCollectionsApi, chatSkillsApi, chatToolsApi, modelMCPServersApi, modelsApi } from '../utils/api'
import { addonOptions, collectionOptions, connectorOptions, emptySources, serverOptions, skillOptions } from '../utils/projectToolSources'

// What the tool defaults of a project can name, read once when the editor
// opens. Each kind loads on its own: a source that fails stays not ready, so
// the editor offers no suggestions for it and claims nothing about its
// entries (they can still be typed).
const MAX_MODELS = 24

export function useProjectToolSources() {
  const [sources, setSources] = useState(emptySources)

  useEffect(() => {
    let live = true
    const put = (kind, options, ready = true) => { if (live) setSources(s => ({ ...s, [kind]: { ready, options } })) }

    chatSkillsApi.list().then(r => put('skills', skillOptions(r))).catch(() => {})
    chatToolsApi.draft('', '').then(p => {
      put('addons', addonOptions(p))
      put('connectors', connectorOptions(p))
    }).catch(() => {})
    agentCollectionsApi.list(false).then(d => put('collections', collectionOptions(d))).catch(() => {})
    // MCP servers belong to a model's configuration: the list is the union
    // over the chat models. If one model's read fails, the union is partial,
    // so the source stays not ready.
    modelsApi.listCapabilities().then(async (data) => {
      const all = (data?.data || []).filter(m => !m.disabled && (m.capabilities || []).some(c => /chat/i.test(c))).map(m => m.id)
      const results = await Promise.allSettled(all.slice(0, MAX_MODELS).map(id => modelMCPServersApi.list(id)))
      const complete = all.length <= MAX_MODELS && results.every(r => r.status === 'fulfilled')
      put('mcp_servers', serverOptions(results.filter(r => r.status === 'fulfilled').map(r => (r.value?.servers || []).map(s => s.name))), complete)
    }).catch(() => {})
    return () => { live = false }
  }, [])

  return sources
}
