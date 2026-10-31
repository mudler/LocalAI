import { useCallback, useEffect, useRef, useState } from 'react'
import { chatAddonsApi, chatSkillsApi, chatToolsApi } from '../utils/api'
import { connectorRef, toggleOff, toolsCounts } from '../utils/chatMCP'
import { changedIds, offList, pickerSkills, revertSkills, skillKey, withSkillsSet } from '../utils/chatSkills'
import { commandsState } from '../utils/chatCommands'
import { applyChoices, draftSwitches as switchesOf, emptyChoices, withChoice } from '../utils/chatDraftSwitches'

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
//
// Before the chat exists (no chatId) draft names the model and the agent of
// the new chat: the lists come from the draft endpoints, a switch changes only
// the screen, and draftSwitches() gives the choices for the create request
// (utils/chatDraftSwitches.js). Choices outlive a change of model or agent.
export function useChatSwitches(chatId, filesOn, draft = null) {
  const draftOn = !chatId && !!draft
  const draftModel = draft?.model || ''
  const draftAgent = draft?.agentId || ''
  const choicesRef = useRef(emptyChoices())
  const choose = useCallback((kind, key, value) => { choicesRef.current = withChoice(choicesRef.current, kind, key, value) }, [])
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
    if (!chatId && !draftOn) { setAddons([]); setCommands(noCommands); setMcp(noMCP); setBuiltins([]); setSkills([]); setReplaced([]); return undefined }
    let live = true
    // A draft list keeps the choices made so far for the rows it still has.
    const chosen = (lists) => (draftOn ? applyChoices(lists, choicesRef.current, connectorRef) : lists)
    ;(draftOn ? chatAddonsApi.draft(draftModel, draftAgent) : chatAddonsApi.list(chatId))
      .then((res) => { if (live) { setAddons(chosen({ addons: res.addons || [] }).addons); setCommands(commandsOf(res)) } })
      .catch(() => { if (live) { setAddons([]); setCommands(noCommands) } })
    ;(draftOn ? chatToolsApi.draft(draftModel, draftAgent) : chatToolsApi.list(chatId))
      .then((res) => {
        if (!live) return
        const m = mcpOf(res)
        const got = chosen({ servers: m.servers, connectors: m.connectors, mcpOff: m.off, builtins: res.builtins || [] })
        setMcp(draftOn ? { servers: got.servers, connectors: got.connectors, off: got.mcpOff } : m)
        setBuiltins(got.builtins)
      })
      .catch(() => { if (live) { setMcp(noMCP); setBuiltins([]) } })
    return () => { live = false }
  }, [chatId, filesOn, draftOn, draftModel, draftAgent])

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

  const toggleAddon = useCallback(async (id, on) => {
    const before = addons
    const next = addons.map((a) => (a.id === id ? { ...a, off: !on } : a))
    setAddons(next)
    setError('')
    if (draftOn) { choose('addons', id, !on); return }
    try {
      const res = await chatAddonsApi.setOff(chatId, next.filter((a) => a.off).map((a) => a.id).sort())
      if (res?.addons) {
        setAddons(res.addons)
        setCommands(commandsOf(res))
      }
      setSkillsRev((n) => n + 1)
    } catch (e) {
      setAddons(before)
      setError(e.message || 'error')
    }
  }, [addons, chatId, draftOn, choose])

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
    if (draftOn) { choose('mcp', key, !on); return }
    try {
      const res = await chatToolsApi.setMcpOff(chatId, off)
      setMcp(mcpOf(res))
      setBuiltins(res.builtins || [])
    } catch (e) {
      setMcp(before)
      setError(e.message || 'error')
    }
  }, [mcp, chatId, draftOn, choose])

  // group is a group of built-in tools, such as web. An opt-in group
  // (saved_agents) is off until the chat switches it on, so it goes in the
  // list of groups that are on instead.
  const toggleBuiltin = useCallback(async (group, on) => {
    const before = builtins
    const next = builtins.map((b) => (b.group === group ? { ...b, off: !on } : b))
    setBuiltins(next)
    setError('')
    if (draftOn) { choose('builtins', group, !on); return }
    try {
      const off = next.filter((b) => b.off && !b.opt_in).map((b) => b.group).sort()
      const optIn = next.filter((b) => b.opt_in)
      const onList = optIn.length ? optIn.filter((b) => !b.off).map((b) => b.group).sort() : undefined
      const res = await chatToolsApi.setBuiltinsOff(chatId, off, onList)
      setBuiltins(res.builtins || [])
      setMcp(mcpOf(res))
    } catch (e) {
      setBuiltins(before)
      setError(e.message || 'error')
    }
  }, [builtins, chatId, draftOn, choose])

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
    draftSwitches, resetDraft, draft: draftOn,
    addons, builtins, commands, servers: mcp.servers, connectors: mcp.connectors, skills, replacedSkills: replaced, error, skillsError, skillsBusy: skillsBusy > 0,
    counts: toolsCounts({ builtins, addons, servers: mcp.servers, connectors: mcp.connectors }),
    toggleAddon, toggleBuiltin, toggleMCP, toggleSkill, setSkillsOn,
  }
}
