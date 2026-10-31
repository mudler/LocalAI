import { useCallback, useEffect, useRef, useState } from 'react'
import { chatAddonsApi, chatSkillsApi, chatToolsApi } from '../utils/api'
import { connectorRef, toolsCounts } from '../utils/chatMCP'
import { offWith, withOn } from '../utils/chatSwitchRows'
import { changedIds, offList, pickerSkills, revertSkills, skillKey, withSkillsSet } from '../utils/chatSkills'
import { commandsState } from '../utils/chatCommands'

const noMCP = { servers: [], connectors: [], off: [] }
const mcpOf = (res) => ({ servers: res.servers || [], connectors: res.connectors || [], off: res.off || [] })
const noCommands = { state: '', reason: '' }
// The commands state of the list: see commandsState in utils/chatCommands.js.
const commandsOf = (res) => commandsState(res)

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
  // The built-in skills an add-on replaces now: shown dimmed, with no switch.
  const [replaced, setReplaced] = useState([])
  // A change of the add-ons or of the skills can bring a built-in skill back,
  // so the list is read again after one.
  const [skillsRev, setSkillsRev] = useState(0)
  const [error, setError] = useState('')
  const [skillsError, setSkillsError] = useState('')
  const [skillsBusy, setSkillsBusy] = useState(0)
  // The skills as the screen shows them now, for requests that start before a render.
  const skillsRef = useRef([])
  skillsRef.current = skills

  useEffect(() => {
    setError('')
    setSkillsError('')
    if (!chatId) { setAddons([]); setCommands(noCommands); setMcp(noMCP); setBuiltins([]); setSkills([]); setReplaced([]); return undefined }
    let live = true
    chatAddonsApi.list(chatId)
      .then((res) => { if (live) { setAddons(res.addons || []); setCommands(commandsOf(res)) } })
      .catch(() => { if (live) { setAddons([]); setCommands(noCommands) } })
    chatToolsApi.list(chatId)
      .then((res) => { if (live) { setMcp(mcpOf(res)); setBuiltins(res.builtins || []) } })
      .catch(() => { if (live) { setMcp(noMCP); setBuiltins([]) } })
    return () => { live = false }
  }, [chatId, filesOn])

  useEffect(() => {
    if (!chatId || !filesOn) { setSkills([]); setReplaced([]); return undefined }
    let live = true
    // Only the skill that wins has a switch: one that another overrides cannot
    // be used anyway. A built-in skill an add-on replaces is shown apart.
    chatSkillsApi.list(chatId)
      .then((res) => {
        if (!live) return
        const picked = pickerSkills(res.skills)
        setSkills(picked.skills)
        setReplaced(picked.replaced)
      })
      .catch(() => { if (live) { setSkills([]); setReplaced([]) } })
    return () => { live = false }
  }, [chatId, filesOn, skillsRev])

  // The lists as the screen shows them now, for a change that starts before a
  // render: "Select all" in the Tools menu sends one request per kind in a row.
  const addonsRef = useRef(addons)
  const mcpRef = useRef(mcp)
  const builtinsRef = useRef(builtins)
  addonsRef.current = addons
  mcpRef.current = mcp
  builtinsRef.current = builtins
  const putAddons = (v) => { addonsRef.current = v; setAddons(v) }
  const putMcp = (v) => { mcpRef.current = v; setMcp(v) }
  const putBuiltins = (v) => { builtinsRef.current = v; setBuiltins(v) }

  // Switches the add-ons with these ids (an array) on or off with one request.
  const setAddonsOn = useCallback(async (ids, on) => {
    const before = addonsRef.current
    const next = withOn(before, (a) => a.id, new Set(ids), on)
    if (ids.length === 0) return
    putAddons(next)
    setError('')
    try {
      const res = await chatAddonsApi.setOff(chatId, next.filter((a) => a.off).map((a) => a.id).sort())
      if (res?.addons) {
        putAddons(res.addons)
        setCommands(commandsOf(res))
      }
      setSkillsRev((n) => n + 1)
    } catch (e) {
      putAddons(before)
      setError(e.message || 'error')
    }
  }, [chatId])

  // keys: server names of the model or connector:<id>, switched with one request.
  const setMCPOn = useCallback(async (keys, on) => {
    const before = mcpRef.current
    if (keys.length === 0) return
    const off = offWith(before.off, keys, on)
    putMcp({
      servers: before.servers.map((s) => (keys.includes(s.name) ? { ...s, off: !on } : s)),
      connectors: before.connectors.map((c) => (keys.includes(connectorRef(c.id)) ? { ...c, off: !on } : c)),
      off,
    })
    setError('')
    try {
      const res = await chatToolsApi.setMcpOff(chatId, off)
      putMcp(mcpOf(res))
      putBuiltins(res.builtins || [])
    } catch (e) {
      putMcp(before)
      setError(e.message || 'error')
    }
  }, [chatId])

  // groups of built-in tools, such as web. An opt-in group (saved_agents) is
  // off until the chat switches it on, so it goes in the list of groups that
  // are on instead.
  const setBuiltinsOn = useCallback(async (groups, on) => {
    const before = builtinsRef.current
    if (groups.length === 0) return
    const next = withOn(before, (b) => b.group, new Set(groups), on)
    putBuiltins(next)
    setError('')
    try {
      const off = next.filter((b) => b.off && !b.opt_in).map((b) => b.group).sort()
      const optIn = next.filter((b) => b.opt_in)
      const onList = optIn.length ? optIn.filter((b) => !b.off).map((b) => b.group).sort() : undefined
      const res = await chatToolsApi.setBuiltinsOff(chatId, off, onList)
      putBuiltins(res.builtins || [])
      putMcp(mcpOf(res))
    } catch (e) {
      putBuiltins(before)
      setError(e.message || 'error')
    }
  }, [chatId])

  const toggleAddon = useCallback((id, on) => setAddonsOn([id], on), [setAddonsOn])
  const toggleMCP = useCallback((key, on) => setMCPOn([key], on), [setMCPOn])
  const toggleBuiltin = useCallback((group, on) => setBuiltinsOn([group], on), [setBuiltinsOn])

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
      // An add-on skill off brings back the built-in skill it replaces, and on
      // hides it again: the list is read again so the picker shows which.
      if (changedIds(before, ids, on).some((id) => String(id).startsWith('addon:'))) setSkillsRev((n) => n + 1)
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
    addons, builtins, commands, servers: mcp.servers, connectors: mcp.connectors, skills, replacedSkills: replaced, error, skillsError, skillsBusy: skillsBusy > 0,
    counts: toolsCounts({ builtins, addons, servers: mcp.servers, connectors: mcp.connectors }),
    toggleAddon, toggleBuiltin, toggleMCP, toggleSkill, setSkillsOn, setAddonsOn, setBuiltinsOn, setMCPOn,
  }
}
