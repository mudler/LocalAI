import { useCallback, useEffect, useState } from 'react'
import { chatAddonsApi, chatSkillsApi, chatToolsApi } from '../utils/api'
import { connectorRef, toggleOff, toolsCounts } from '../utils/chatMCP'

const noMCP = { servers: [], connectors: [], off: [] }

// What a chat can use besides its built-in tools, with a switch each: add-ons,
// the MCP servers of its model, the user's MCP connectors and the skills. A
// chat with an agent lists only what the agent grants. A switch changes the
// list at once and goes back when the server refuses.
export function useChatSwitches(chatId, filesOn) {
  const [addons, setAddons] = useState([])
  const [mcp, setMcp] = useState(noMCP)
  const [skills, setSkills] = useState([])
  const [error, setError] = useState('')

  useEffect(() => {
    setError('')
    if (!chatId) { setAddons([]); setMcp(noMCP); setSkills([]); return undefined }
    let live = true
    chatAddonsApi.list(chatId)
      .then((res) => { if (live) setAddons(res.addons || []) })
      .catch(() => { if (live) setAddons([]) })
    chatToolsApi.list(chatId)
      .then((res) => { if (live) setMcp({ servers: res.servers || [], connectors: res.connectors || [], off: res.off || [] }) })
      .catch(() => { if (live) setMcp(noMCP) })
    if (filesOn) {
      // Only the skill that wins is listed: one that another overrides cannot be used anyway.
      chatSkillsApi.list(chatId)
        .then((res) => { if (live) setSkills((res.skills || []).filter((s) => !s.shadowed && !s.invalid)) })
        .catch(() => { if (live) setSkills([]) })
    } else {
      setSkills([])
    }
    return () => { live = false }
  }, [chatId, filesOn])

  const toggleAddon = useCallback(async (id, on) => {
    const before = addons
    const next = addons.map((a) => (a.id === id ? { ...a, off: !on } : a))
    setAddons(next)
    setError('')
    try {
      const res = await chatAddonsApi.setOff(chatId, next.filter((a) => a.off).map((a) => a.id).sort())
      if (res?.addons) setAddons(res.addons)
    } catch (e) {
      setAddons(before)
      setError(e.message || 'error')
    }
  }, [addons, chatId])

  // key is a server name of the model or connector:<id>.
  const toggleMCP = useCallback(async (key, on) => {
    const before = mcp
    const off = toggleOff(mcp.off, key, on)
    setMcp({
      servers: mcp.servers.map((s) => (s.name === key ? { ...s, off: !on } : s)),
      connectors: mcp.connectors.map((c) => (connectorRef(c.id) === key ? { ...c, off: !on } : c)),
      off,
    })
    setError('')
    try {
      const res = await chatToolsApi.setMcpOff(chatId, off)
      setMcp({ servers: res.servers || [], connectors: res.connectors || [], off: res.off || [] })
    } catch (e) {
      setMcp(before)
      setError(e.message || 'error')
    }
  }, [mcp, chatId])

  const toggleSkill = useCallback(async (name, on) => {
    const before = skills
    const next = skills.map((s) => (s.name === name ? { ...s, enabled: on } : s))
    setSkills(next)
    setError('')
    try {
      await chatSkillsApi.setOff(chatId, next.filter((s) => !s.enabled).map((s) => s.name).sort())
    } catch (e) {
      setSkills(before)
      setError(e.message || 'error')
    }
  }, [skills, chatId])

  return {
    addons, servers: mcp.servers, connectors: mcp.connectors, skills, error,
    counts: toolsCounts({ addons, servers: mcp.servers, connectors: mcp.connectors }),
    toggleAddon, toggleMCP, toggleSkill,
  }
}
