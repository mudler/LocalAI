import { useCallback, useEffect, useRef, useState } from 'react'
import { chatAddonsApi, chatSkillsApi, chatToolsApi } from '../utils/api'
import { connectorRef, toolsCounts } from '../utils/chatMCP'
import { offWith, withOn } from '../utils/chatSwitchRows'
import { changedIds, offList, pickerSkills, revertSkills, skillKey, withSkillsSet } from '../utils/chatSkills'
import { commandsState, noticeIdentity } from '../utils/chatCommands'
import { applyChoices, draftListSwitches, draftSwitches as switchesOf, emptyChoices, withChoice } from '../utils/chatDraftSwitches'

const noMCP = { servers: [], connectors: [], off: [] }
const mcpOf = (res) => ({ servers: res.servers || [], connectors: res.connectors || [], off: res.off || [] })
const noCommands = { state: 'unknown', reason: '', identity: '' }
// The commands state of the list: see commandsState in utils/chatCommands.js.
const commandsOf = (res) => ({ ...commandsState(res), provider: typeof res?.commands_provider === 'string' ? res.commands_provider : '', hostExec: typeof res?.commands_host_exec === 'string' ? res.commands_host_exec : '', identity: noticeIdentity(res, '', '') })

export { skillKey }

// What a chat can use besides its own tools, with a switch each: the groups of
// built-in tools (web), add-ons, the MCP servers of its model, the user's MCP
// connectors and the skills. A
// chat with an agent lists only what the agent grants. A switch changes the
// list at once and goes back when the server refuses.
//
// Before the chat exists (no chatId) draft names the model and the agent of
// the new chat: the lists come from the draft endpoints, a switch changes only
// the screen, and draftSwitches() gives the choices for the create request
// (utils/chatDraftSwitches.js). Choices outlive a change of model or agent.
export function useChatSwitches(chatId, filesOn, draft = null, actorSession = '') {
  const draftOn = !chatId && !!draft
  const draftModel = draft?.model || ''
  const draftAgent = draft?.agentId || ''
  const choicesRef = useRef(emptyChoices())
  const choose = useCallback((kind, key, value) => { choicesRef.current = withChoice(choicesRef.current, kind, key, value) }, [])
  const [addons, setAddons] = useState([])
  // Whether the chat can run commands, from the same list (see utils/chatCommands.js).
  const commandEpoch = useRef(0)
  const context = JSON.stringify([actorSession, chatId, draftModel, draftAgent, filesOn])
  const commandContext = useRef(context)
  if (commandContext.current !== context) { commandContext.current = context; commandEpoch.current++ }
  const [commands, storeCommands] = useState(noCommands)
  const setCommands = (value) => storeCommands({ ...value, context: commandContext.current })
  const [mcp, setMcp] = useState(noMCP)
  const [builtins, setBuiltins] = useState([])
  // The built-in groups as the screen shows them now (also read by the effects below).
  const builtinsRef = useRef(builtins)
  // The built-in tools an installed add-on replaces but cannot serve here now.
  const [fallbacks, setFallbacks] = useState([])
  // Whether memory works in the chat and which level turned it off (the memory
  // object of the tools list), or null before the list is read.
  const [memory, setMemory] = useState(null)
  const [skills, setSkills] = useState([])
  // The built-in skills an add-on replaces now: shown dimmed, with no switch.
  const [replaced, setReplaced] = useState([])
  // A change of the add-ons or of the skills can bring a built-in skill back,
  // so the list is read again after one.
  const [skillsRev, setSkillsRev] = useState(0)
  // Counts the changes after which what the chat can run changed: the network
  // controls of the chat (its egress view) are read again.
  const [commandsRev, setCommandsRev] = useState(0)
  const [error, setError] = useState('')
  const [skillsError, setSkillsError] = useState('')
  const [skillsBusy, setSkillsBusy] = useState(0)
  // The skills as the screen shows them now, for requests that start before a render.
  const skillsRef = useRef([])
  skillsRef.current = skills

  useEffect(() => {
    setError('')
    setSkillsError('')
    if (!chatId && !draftOn) { setAddons([]); setCommands(noCommands); setMcp(noMCP); setBuiltins([]); setFallbacks([]); setMemory(null); setSkills([]); setReplaced([]); return undefined }
    let live = true
    const epoch = ++commandEpoch.current
    setCommands(noCommands)
    // A draft list keeps the choices made so far for the rows it still has.
    const chosen = (lists) => (draftOn ? applyChoices(lists, choicesRef.current, connectorRef) : lists)
    ;(draftOn ? chatAddonsApi.draft(draftModel, draftAgent, draftListSwitches(choicesRef.current, builtinsRef.current)) : chatAddonsApi.list(chatId))
      .then((res) => { if (live && epoch === commandEpoch.current) { setAddons(chosen({ addons: res.addons || [] }).addons); setCommands(commandsOf(res)) } })
      .catch(() => { if (live && epoch === commandEpoch.current) { setAddons([]); setCommands(noCommands) } })
    ;(draftOn ? chatToolsApi.draft(draftModel, draftAgent) : chatToolsApi.list(chatId))
      .then((res) => {
        if (!live) return
        const m = mcpOf(res)
        const got = chosen({ servers: m.servers, connectors: m.connectors, mcpOff: m.off, builtins: res.builtins || [] })
        setMcp(draftOn ? { servers: got.servers, connectors: got.connectors, off: got.mcpOff } : m)
        setBuiltins(got.builtins)
        setFallbacks(res.fallbacks || [])
        setMemory(res.memory || null)
      })
      .catch(() => { if (live) { setMcp(noMCP); setBuiltins([]); setFallbacks([]); setMemory(null) } })
    return () => { live = false }
  }, [chatId, filesOn, draftOn, draftModel, draftAgent, actorSession])

  useEffect(() => {
    if ((!chatId && !draftOn) || !filesOn) { setSkills([]); setReplaced([]); return undefined }
    let live = true
    // Only the skill that wins has a switch: one that another overrides cannot
    // be used anyway. A built-in skill an add-on replaces is shown apart. The
    // list without a chat is the skills with their default switches.
    chatSkillsApi.list(chatId || undefined)
      .then((res) => {
        if (!live) return
        const picked = pickerSkills(res.skills)
        setSkills(draftOn ? applyChoices({ skills: picked.skills }, choicesRef.current).skills : picked.skills)
        setReplaced(picked.replaced)
      })
      .catch(() => { if (live) { setSkills([]); setReplaced([]) } })
    return () => { live = false }
  }, [chatId, filesOn, skillsRev, draftOn])

  // The lists as the screen shows them now, for a change that starts before a
  // render: "Select all" in the Tools menu sends one request per kind in a row.
  const addonsRef = useRef(addons)
  const mcpRef = useRef(mcp)
  addonsRef.current = addons
  mcpRef.current = mcp
  builtinsRef.current = builtins
  const putAddons = (v) => { addonsRef.current = v; setAddons(v) }
  const putMcp = (v) => { mcpRef.current = v; setMcp(v) }
  const putBuiltins = (v) => { builtinsRef.current = v; setBuiltins(v) }

  // A draft has no status of its own: the commands state is what the server
  // answers for the draft's choices so far. Asked again after a switch of the
  // add-ons or of the built-in groups, so a sandbox switched off shows its
  // notice before the first message. The old state stays until the answer
  // comes; a later switch drops an answer that is late.
  const refreshDraftCommands = useCallback(() => {
    const epoch = ++commandEpoch.current
    chatAddonsApi.draft(draftModel, draftAgent, draftListSwitches(choicesRef.current, builtinsRef.current))
      .then((res) => { if (epoch === commandEpoch.current) setCommands(commandsOf(res)) })
      .catch(() => { if (epoch === commandEpoch.current) setCommands(noCommands) })
  }, [draftModel, draftAgent])

  // Switches the add-ons with these ids (an array) on or off with one request.
  const setAddonsOn = useCallback(async (ids, on) => {
    const before = addonsRef.current
    const next = withOn(before, (a) => a.id, new Set(ids), on)
    if (ids.length === 0) return
    putAddons(next)
    setError('')
    // A draft keeps the state it had until the server answers for the new
    // choices (refreshDraftCommands): a reset here would leave the notice unknown.
    if (draftOn) {
      ids.forEach((id) => choose('addons', id, !on))
      setSkillsRev((n) => n + 1)
      refreshDraftCommands()
      return
    }
    const epoch = ++commandEpoch.current
    setCommands(noCommands)
    try {
      const res = await chatAddonsApi.setOff(chatId, next.filter((a) => a.off).map((a) => a.id).sort())
      if (epoch !== commandEpoch.current) return
      if (res?.addons) {
        putAddons(res.addons)
        setCommands(commandsOf(res))
      }
      setSkillsRev((n) => n + 1)
      setCommandsRev((n) => n + 1)
      // An add-on switched off or on changes who serves the tools it
      // replaces, and the notes about them.
      chatToolsApi.list(chatId)
        .then((r) => { putBuiltins(r.builtins || []); setFallbacks(r.fallbacks || []) })
        .catch(() => {})
    } catch (e) {
      if (epoch !== commandEpoch.current) return
      setCommands(noCommands)
      putAddons(before)
      setError(e.message || 'error')
    }
  }, [chatId, draftOn, choose, refreshDraftCommands])

  // Sets the connections (binding IDs, an empty list to clear) this chat may
  // use for the tools of an add-on that runs with a credential. Enabling any
  // switches the add-on on. A draft keeps the choice for the create request.
  const setConnections = useCallback(async (addonId, connections) => {
    const before = addonsRef.current
    const row = before.find((a) => a.id === addonId)
    if (!row || row.connection_by === 'agent') return
    // The list of the row decides what exists and in which order.
    const ids = (row.connections || []).map((c) => c.id).filter((id) => connections.includes(id))
    const only = (row.connections || []).length === 1
    putAddons(before.map((a) => (a.id !== addonId ? a : {
      ...a, enabled_connections: ids.length ? ids : (only ? a.enabled_connections : []),
      connection_by: ids.length ? 'chat' : (only ? a.connection_by : ''), needs_connection: ids.length === 0 && !only, off: ids.length ? false : a.off,
    })))
    setError('')
    if (draftOn) {
      choose('connections', addonId, ids)
      if (ids.length) choose('addons', addonId, false)
      setSkillsRev((n) => n + 1)
      refreshDraftCommands()
      return
    }
    const epoch = ++commandEpoch.current
    try {
      const res = await chatAddonsApi.setConnections(chatId, addonId, ids)
      if (epoch !== commandEpoch.current) return
      if (res?.addons) {
        putAddons(res.addons)
        setCommands(commandsOf(res))
      }
      setSkillsRev((n) => n + 1)
      setCommandsRev((n) => n + 1)
    } catch (e) {
      if (epoch !== commandEpoch.current) return
      putAddons(before)
      setError(e.message || 'error')
    }
  }, [chatId, draftOn, choose, refreshDraftCommands])

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
    if (draftOn) { keys.forEach((key) => choose('mcp', key, !on)); return }
    try {
      const res = await chatToolsApi.setMcpOff(chatId, off)
      putMcp(mcpOf(res))
      putBuiltins(res.builtins || [])
      setFallbacks(res.fallbacks || [])
    } catch (e) {
      putMcp(before)
      setError(e.message || 'error')
    }
  }, [chatId, draftOn, choose])

  // groups of built-in tools, such as web. An opt-in group (saved_agents) is
  // off until the chat switches it on, so it goes in the list of groups that
  // are on instead.
  const setBuiltinsOn = useCallback(async (groups, on) => {
    const before = builtinsRef.current
    if (groups.length === 0) return
    const next = withOn(before, (b) => b.group, new Set(groups), on)
    putBuiltins(next)
    setError('')
    // A draft keeps the state it had until the server answers for the new
    // choices (refreshDraftCommands).
    if (draftOn) { groups.forEach((group) => choose('builtins', group, !on)); refreshDraftCommands(); return }
    const epoch = ++commandEpoch.current
    setCommands(noCommands)
    try {
      const off = next.filter((b) => b.off && !b.opt_in).map((b) => b.group).sort()
      const optIn = next.filter((b) => b.opt_in)
      const onList = optIn.length ? optIn.filter((b) => !b.off).map((b) => b.group).sort() : undefined
      const res = await chatToolsApi.setBuiltinsOff(chatId, off, onList)
      const status = await chatAddonsApi.list(chatId)
      if (epoch !== commandEpoch.current) return
      setCommands(commandsOf(status))
      setCommandsRev((n) => n + 1)
      putBuiltins(res.builtins || [])
      putMcp(mcpOf(res))
      setFallbacks(res.fallbacks || [])
      setMemory(res.memory || null)
    } catch (e) {
      if (epoch !== commandEpoch.current) return
      setCommands(noCommands)
      putBuiltins(before)
      setError(e.message || 'error')
    }
  }, [chatId, draftOn, choose, refreshDraftCommands])

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
    if (draftOn) {
      for (const id of changedIds(before, ids, on)) choose('skills', id, on)
      return
    }
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
  }, [chatId, draftOn, choose])

  // A switch names the skill by its id ("layer:name"): a plain name would
  // turn off whichever skill of that name wins, not this one.
  const toggleSkill = useCallback((id, on) => setSkillsOn(new Set([id]), on), [setSkillsOn])

  // The choices of a draft, for the create request; null when there is none.
  const draftSwitches = useCallback(
    () => (draftOn ? switchesOf({ addons, mcpOff: mcp.off, builtins, skills: skillsRef.current }) : null),
    [draftOn, addons, mcp.off, builtins],
  )
  // A chat was created with the draft: the next draft starts from the defaults.
  const resetDraft = useCallback(() => { choicesRef.current = emptyChoices() }, [])

  return {
    draftSwitches, resetDraft, draft: draftOn, commandsRev,
    addons, builtins, fallbacks, memory, commands: commands.context === context ? commands : noCommands, servers: mcp.servers, connectors: mcp.connectors, skills, replacedSkills: replaced, error, skillsError, skillsBusy: skillsBusy > 0,
    counts: toolsCounts({ builtins, addons, servers: mcp.servers, connectors: mcp.connectors }),
    toggleAddon, toggleBuiltin, toggleMCP, toggleSkill, setSkillsOn, setAddonsOn, setBuiltinsOn, setMCPOn, setConnections,
  }
}
