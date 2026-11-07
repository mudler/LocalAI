import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelMCPServersApi } from '../utils/api'
import { filesChoiceOf, withFilesChoice } from '../utils/chatAgents'
import { activePreset, PRESET_KEYS, presetPatch, snapshot } from '../utils/agentPresets'
import { accountsAfter, addonSwitch, mcpRows, mcpUnknown, resourcesAfter } from '../utils/agentUses'
import { joinAgentMCP, splitAgentMCP } from '../utils/chatMCP'

// What the agent form does with the tool lists of useAgentTools: the capability
// levels with their way back, the Files choice, and the add-on, account,
// resource and MCP switches. The full editor and the new-agent wizard both use
// it, so a level, a notice and an undo behave the same in both.
//
//   form, setForm, updateField   the form state of the page
//   tools                        the result of useAgentTools(form, updateField)
//   initialFolders               the folder rows as read (none for a new agent)
export function useAgentAbilities({ form, setForm, updateField, tools, initialFolders }) {
  const { t } = useTranslation('agents')
  // What a level or template just filled in, with the values to go back to.
  const [applied, setApplied] = useState(null)
  const [modelServers, setModelServers] = useState([])
  // null, a row index, or 'new' for a row being added
  const [choosingFolder, setChoosingFolder] = useState(null)

  // The servers of the fixed model are suggestions; with no fixed model the field is free text.
  const fixedModel = form.model || ''
  useEffect(() => {
    if (!fixedModel) { setModelServers([]); return undefined }
    let live = true
    modelMCPServersApi.list(fixedModel).then(r => { if (live) setModelServers((r.servers || []).map(s => s.name).filter(Boolean)) }).catch(() => { if (live) setModelServers([]) })
    return () => { live = false }
  }, [fixedModel])

  // The Files choice: No files, All my files or Only these folders. It sets
  // the Files tool switch and the folder rows together (see withFilesChoice).
  const filesChoice = filesChoiceOf(form)
  const setScope = (choice) => setForm(prev => withFilesChoice(prev, choice, initialFolders))
  // A level or template that leaves the Files tool off is No files: the folder
  // rows go with it, so the choice reads the same everywhere. One that leaves
  // it on keeps the rows (Full never wipes them).
  const withPresetFiles = (next) => (filesChoiceOf(next) === 'none' ? { ...next, folders: [] } : next)
  const setRow = (i, patch) => updateField('folders', form.folders.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const removeRow = (i) => updateField('folders', form.folders.filter((_, j) => j !== i))
  const addRow = () => { updateField('folders', [...form.folders, { mode: 'read' }]); setChoosingFolder('new') }

  // The level of the tool switches. A level applied by hand stays in the form
  // until Save and can be undone.
  const toolGroupsList = tools.builtins
  const grantCatalog = tools.grants?.list || []
  const levelReady = toolGroupsList.length > 0
  const level = activePreset(form, toolGroupsList, grantCatalog)
  const applyLevel = (pid) => {
    const patch = presetPatch(pid, form, toolGroupsList, grantCatalog)
    if (!patch) return
    setApplied({ kind: 'preset', id: pid, name: t(`create.preset.${pid}.title`), before: snapshot(form, [...PRESET_KEYS, 'folders']) })
    setForm(prev => withPresetFiles({ ...prev, ...patch }))
  }
  const undoApplied = () => {
    if (!applied) return
    setForm(prev => ({ ...prev, ...applied.before }))
    setApplied(null)
  }

  // An add-on switched on turns on the skills that come with it, with the same
  // way back as a level. Switching it off turns off only those.
  const titleOfAddon = (id) => tools.addons.find(a => a.id === id)?.title || id
  const noteAddonOn = (id, turned, before) => {
    if (!turned.length) return
    setApplied({ kind: 'addon', id, name: titleOfAddon(id), count: turned.length, names: turned.join(', '), before: snapshot(before, ['addons', 'skills']) })
  }
  const switchAddon = (id, on) => {
    const r = addonSwitch(form, id, on, tools.skillRows)
    // Out of the box an add-on that scopes its resources per agent starts with
    // all of yours ticked, to read: an empty list means none, and a row that is
    // on and can do nothing is a trap. The ticks are in the row to untick.
    const choice = tools.resourceChoices[id]
    const fresh = on && tools.addons.find(a => a.id === id)?.resources && !(form.resources?.[id] || []).length && choice?.status === 'ready' && choice.items.length > 0
    setForm(prev => ({
      ...prev, addons: r.addons, skills: r.skills,
      ...(fresh ? { resources: { ...(prev.resources || {}), [id]: choice.items.map(x => ({ id: x.id, mode: 'read' })) } } : {}),
    }))
    if (on) noteAddonOn(id, r.turned, form)
    else if (applied?.kind === 'addon' && applied.id === id) setApplied(null)
  }
  // Ticking an account switches the add-on on, as the chat does.
  const pickAccounts = (id, ids) => {
    const r = accountsAfter(form, id, ids, tools.skillRows)
    setForm(prev => ({ ...prev, connections: r.connections, ...(r.switchedOn ? { addons: r.addons, skills: r.skills } : {}) }))
    if (r.switchedOn) noteAddonOn(id, r.turned, form)
  }
  const pickResource = (id, resId, mode) => {
    const wasOff = !(form.addons || []).includes(id)
    const resources = resourcesAfter(form, id, resId, mode)
    if (mode && wasOff) {
      const r = addonSwitch(form, id, true, tools.skillRows)
      setForm(prev => ({ ...prev, resources, addons: r.addons, skills: r.skills }))
      noteAddonOn(id, r.turned, form)
    } else {
      setForm(prev => ({ ...prev, resources }))
    }
  }

  // The MCP servers: what the host defines, and what the agent holds that it does not.
  const mcp = splitAgentMCP(form.mcp_servers)
  const mcpList = mcpRows({ connectors: tools.connectors, servers: modelServers, stored: mcp.servers, checkable: !!form.model && modelServers.length > 0 })
  const unknownMcp = mcpUnknown(mcpList)
  const setMcp = (r, on) => {
    if (r.ref.startsWith('connector:')) { tools.toggleMCP(r.ref, on); return }
    const rest = mcp.servers.filter(x => x !== r.id)
    updateField('mcp_servers', joinAgentMCP(on ? [...rest, r.id] : rest, mcp.connectors))
  }

  return {
    applied, setApplied, undoApplied,
    filesChoice, setScope, withPresetFiles, setRow, removeRow, addRow, choosingFolder, setChoosingFolder,
    toolGroupsList, grantCatalog, levelReady, level, applyLevel,
    switchAddon, pickAccounts, pickResource, mcpList, unknownMcp, setMcp,
  }
}
