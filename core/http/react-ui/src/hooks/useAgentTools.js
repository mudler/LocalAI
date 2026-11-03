import { useCallback, useEffect, useMemo, useState } from 'react'
import { chatSkillsApi, chatToolsApi } from '../utils/api'
import { pickerSkills } from '../utils/chatSkills'
import { useBuiltinGrants } from './useBuiltinGrants'
import { addonsAfter, agentAddons, agentGrants, grantsAfter, agentBuiltins, agentConnectors, agentSkills, builtinsAfter, connectionsAfter, connectorsAfter, skillsAfter } from '../utils/agentTools'

// The Tools list of the agent form. It reads what a new chat on the agent's
// model could use (the draft lists, the ones the Tools menu of a new chat
// shows) and applies the agent's grants and switches to them, so the form
// shows the same rows as the chat. A switch changes the form, not the server:
// the agent is saved with the form. It returns the shape useChatSwitches
// returns, so useToolGroups draws both.
//
// form: the agent form; update(field, value) changes one field of it.
export function useAgentTools(form, update) {
  const model = form.model || ''
  const [panel, setPanel] = useState({ builtins: [], addons: [], connectors: [] })
  const [skillList, setSkillList] = useState([])
  const [replaced, setReplaced] = useState([])
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    chatToolsApi.draft(model, '')
      .then((res) => { if (live) { setPanel({ builtins: res.builtins || [], addons: res.addons || [], connectors: res.connectors || [] }); setError('') } })
      .catch((e) => { if (live) { setPanel({ builtins: [], addons: [], connectors: [] }); setError(e.message || 'error') } })
    return () => { live = false }
  }, [model])

  useEffect(() => {
    let live = true
    chatSkillsApi.list()
      .then((res) => {
        if (!live) return
        const picked = pickerSkills(res.skills)
        setSkillList(picked.skills)
        setReplaced(picked.replaced)
      })
      .catch(() => { if (live) { setSkillList([]); setReplaced([]) } })
    return () => { live = false }
  }, [])

  const builtins = useMemo(() => agentBuiltins(panel.builtins, form), [panel.builtins, form])
  const addons = useMemo(() => agentAddons(panel.addons, form), [panel.addons, form])
  const connectors = useMemo(() => agentConnectors(panel.connectors, form), [panel.connectors, form])
  const skills = useMemo(() => agentSkills(skillList, form), [skillList, form])
  const catalog = useBuiltinGrants()
  const grantList = useMemo(() => agentGrants(catalog, form.builtins), [catalog, form.builtins])

  const setBuiltinsOn = useCallback(async (groups, on) => {
    const next = builtinsAfter(form, panel.builtins, groups, on)
    update('builtins_off', next.builtins_off)
    update('builtins_on', next.builtins_on)
  }, [form, panel.builtins, update])
  const setAddonsOn = useCallback(async (ids, on) => { update('addons', addonsAfter(form, ids, on)) }, [form, update])
  const setMCPOn = useCallback(async (keys, on) => { update('mcp_servers', connectorsAfter(form, keys, on)) }, [form, update])
  const setGrantsOn = useCallback(async (names, on) => { update('builtins', grantsAfter(form, names, on)) }, [form, update])
  const setSkillsOn = useCallback(async (ids, on) => { update('skills', skillsAfter(form, skillList, ids, on)) }, [form, skillList, update])
  const setConnections = useCallback((addonId, bindingIds) => update('connections', connectionsAfter(form, addonId, bindingIds)), [form, update])

  return {
    // The grants are rows with their own switch each: no bulk setter, so a
    // click on Select all never grants a sensitive right.
    grants: grantList.length > 0 ? { list: grantList, editable: true, onToggle: (name, on) => setGrantsOn([name], on) } : null,
    builtins, addons, servers: [], connectors, skills, replacedSkills: replaced, error, skillsError: '',
    toggleBuiltin: (g, on) => setBuiltinsOn([g], on),
    toggleAddon: (id, on) => setAddonsOn([id], on),
    toggleMCP: (key, on) => setMCPOn([key], on),
    setBuiltinsOn, setAddonsOn, setMCPOn, setSkillsOn, setConnections,
    commands: { state: 'unknown' },
  }
}
