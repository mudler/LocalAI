import { useCallback, useEffect, useRef, useState } from 'react'
import { chatAddonsApi, chatSkillsApi, chatToolsApi } from '../utils/api'
import { connectorRef, toggleOff, toolsCounts } from '../utils/chatMCP'
import { changedIds, offList, revertSkills, skillKey, withSkillsSet } from '../utils/chatSkills'

const noMCP = { servers: [], connectors: [], off: [] }
const mcpOf = (res) => ({ servers: res.servers || [], connectors: res.connectors || [], off: res.off || [] })
const noCommands = { state: '', reason: '' }
const commandsOf = (res) => ({
  state: typeof res?.commands === 'string' ? res.commands : '',
  reason: typeof res?.commands_reason === 'string' ? res.commands_reason : '',
})

export { skillKey }

// What a chat can use besides its own tools, with a switch each: the groups of
// built-in tools (web), add-ons, the MCP servers of its model, the user's MCP
// connectors and the skills. A
// chat with an agent lists only what the agent grants. A switch changes the
// list at once and goes back when the server refuses.
export function useChatSwitches(chatId, filesOn) {
  const [addons, setAddons] = useState([])
  // Whether the chat can run commands, from the same list (see utils/chatCommands.js).
  const [commands, setCommands] = useState({ state: '', reason: '' })
  const [mcp, setMcp] = useState(noMCP)
  const [builtins, setBuiltins] = useState([])
  const [skills, setSkills] = useState([])
  const [error, setError] = useState('')
  const [skillsError, setSkillsError] = useState('')
  const [skillsBusy, setSkillsBusy] = useState(0)
  // The skills as the screen shows them now, for requests that start before a render.
  const skillsRef = useRef([])
  skillsRef.current = skills

  useEffect(() => {
    setError('')
    setSkillsError('')
    if (!chatId) { setAddons([]); setCommands(noCommands); setMcp(noMCP); setBuiltins([]); setSkills([]); return undefined }
    let live = true
    chatAddonsApi.list(chatId)
      .then((res) => { if (live) { setAddons(res.addons || []); setCommands(commandsOf(res)) } })
      .catch(() => { if (live) { setAddons([]); setCommands(noCommands) } })
    chatToolsApi.list(chatId)
      .then((res) => { if (live) { setMcp(mcpOf(res)); setBuiltins(res.builtins || []) } })
      .catch(() => { if (live) { setMcp(noMCP); setBuiltins([]) } })
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
      if (res?.addons) {
        setAddons(res.addons)
        setCommands(commandsOf(res))
      }
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
      setMcp(mcpOf(res))
      setBuiltins(res.builtins || [])
    } catch (e) {
      setMcp(before)
      setError(e.message || 'error')
    }
  }, [mcp, chatId])

  // group is a group of built-in tools, such as web.
  const toggleBuiltin = useCallback(async (group, on) => {
    const before = builtins
    const next = builtins.map((b) => (b.group === group ? { ...b, off: !on } : b))
    setBuiltins(next)
    setError('')
    try {
      const res = await chatToolsApi.setBuiltinsOff(chatId, next.filter((b) => b.off).map((b) => b.group).sort())
      setBuiltins(res.builtins || [])
      setMcp(mcpOf(res))
    } catch (e) {
      setBuiltins(before)
      setError(e.message || 'error')
    }
  }, [builtins, chatId])

  // Switches the skills with these ids (a Set) on or off with one request: the
  // server stores the full list of skills that are off. The screen changes at
  // once; when the server refuses, only these skills go back and the message
  // says so.
  const setSkillsOn = useCallback(async (ids, on) => {
    const before = skillsRef.current
    if (changedIds(before, ids, on).length === 0) return
    const next = withSkillsSet(before, ids, on)
    skillsRef.current = next
    setSkills(next)
    setSkillsError('')
    setSkillsBusy((n) => n + 1)
    try {
      await chatSkillsApi.setOff(chatId, offList(next))
    } catch (e) {
      const back = revertSkills(skillsRef.current, before, ids)
      skillsRef.current = back
      setSkills(back)
      setSkillsError(e.message || 'error')
    } finally {
      setSkillsBusy((n) => n - 1)
    }
  }, [chatId])

  // A switch names the skill by its id ("layer:name"): a plain name would
  // turn off whichever skill of that name wins, not this one.
  const toggleSkill = useCallback((id, on) => setSkillsOn(new Set([id]), on), [setSkillsOn])

  return {
    addons, builtins, commands, servers: mcp.servers, connectors: mcp.connectors, skills, error, skillsError, skillsBusy: skillsBusy > 0,
    counts: toolsCounts({ builtins, addons, servers: mcp.servers, connectors: mcp.connectors }),
    toggleAddon, toggleBuiltin, toggleMCP, toggleSkill, setSkillsOn,
  }
}
