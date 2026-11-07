import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useNavigate, useLocation, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentCollectionsApi, chatAgentsApi } from '../utils/api'
import { useAgentTools } from '../hooks/useAgentTools'
import { useModels } from '../hooks/useModels'
import { useToolGroups } from '../hooks/useToolGroups'
import { useMemorySettings } from '../hooks/useMemorySettings'
import { useAgentAccess } from '../hooks/useAgentAccess'
import { effectiveLines, flatRows, blockedCount } from '../utils/agentEffective'
import { diffConfig, displayValue, maskSecrets, summarise } from '../utils/agentConfigTools'
import { agentPayload, emptyAgent, filesChoiceOf, folderRows, folderState, foldersUnchosen, memoryChoiceOf, memoryLevelOf, memorySaveOf, missingConnectorGrants, newAgent, setGrant, withMemoryChoice, withMemoryLevel } from '../utils/chatAgents'
import { snapshot } from '../utils/agentPresets'
import { addonNow, memorySkillsState } from '../utils/agentUses'
import { modelCaps } from '../utils/modelCaps'
import { describeChatModel } from '../utils/describeModel'
import { downloadAgentFile } from '../utils/agentFiles'
import './agents.css'
import './library.css'
import '../components/home/composer.css'
// eslint-disable-next-line no-unused-vars
import ModelPicker from '../components/ModelPicker'
// eslint-disable-next-line no-unused-vars
import NameList from '../components/ChatNameList'
// eslint-disable-next-line no-unused-vars
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import { CAP_CHAT } from '../utils/capabilities'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, LoadError, Page, Segmented, SkeletonRows } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import AgentFolderDialog from '../components/agents/AgentFolderDialog'
// eslint-disable-next-line no-unused-vars
import AgentMemoryChoice from '../components/agents/AgentMemoryChoice'
// eslint-disable-next-line no-unused-vars
import { SwitchPanel } from '../components/chat/SwitchMenu'
import { switchCounts, switchRows } from '../utils/chatSwitchRows'
// eslint-disable-next-line no-unused-vars
import { SkillsPanel } from '../components/chat/SkillsMenu'
// eslint-disable-next-line no-unused-vars
import AgentPresets, { AppliedNotice } from '../components/agents/AgentPresets'
// eslint-disable-next-line no-unused-vars
import AgentConnected from '../components/agents/AgentConnected'
// eslint-disable-next-line no-unused-vars
import AgentUsesPanel from '../components/agents/AgentUsesPanel'
// eslint-disable-next-line no-unused-vars
import AgentFiles from '../components/agents/AgentFiles'
// eslint-disable-next-line no-unused-vars
import AgentWizard from './AgentWizard'
import { useAgentAbilities } from '../hooks/useAgentAbilities'
// eslint-disable-next-line no-unused-vars
import AgentMemorySkills, { memorySkillFrom } from '../components/agents/AgentMemorySkills'
// eslint-disable-next-line no-unused-vars
import AgentEffective from '../components/agents/AgentEffective'
// eslint-disable-next-line no-unused-vars
import AgentTryIt from '../components/agents/AgentTryIt'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
import { useSheetFocus } from '../components/agents/useSheetFocus'

// The cards of the page, in order. `bare` parts have no heading of their own:
// the card heading says what they are. `export` exists only for a saved agent.
const GROUPS = [
  { id: 'identity', icon: 'info', parts: ['BasicInfo', 'ModelSettings', 'PromptsGoals', 'Connected'], bare: true },
  { id: 'capabilities', icon: 'plug', parts: ['Uses', 'Skills', 'Files'] },
  { id: 'memory', icon: 'memory', parts: ['Notes', 'MemorySettings'] },
  { id: 'effective', icon: 'eye', parts: ['Effective'], bare: true },
  { id: 'advanced', icon: 'server', parts: ['export'], collapsible: true },
]
const secDomId = (gid) => `aed-sec-${gid}`

// Two letters for the avatar tile of the header: the start of the name.
function monogram(name) {
  const words = (name || '').match(/[\p{L}\p{N}]+/gu) || []
  if (!words.length) return ''
  const letters = words.length > 1 ? words[0][0] + words[1][0] : words[0].slice(0, 2)
  return letters.toUpperCase()
}

// The fields each fold summarises on its closed line.
const SUMMARY_FIELDS = {
  BasicInfo: [{ name: 'name', type: 'text' }, { name: 'description', type: 'textarea' }],
  ModelSettings: [{ name: 'model', type: 'text' }],
  PromptsGoals: [{ name: 'instructions', type: 'textarea' }],
}

// --- Main component ---

// The new-agent route is the wizard. A saved agent, and an agent read from a
// file (it arrives with its fields in the router state), keep the full editor.
export default function AgentCreate() {
  const { id } = useParams()
  const location = useLocation()
  if (!id && !location.state?.importedConfig) return <AgentWizard />
  return <AgentEditor />
}

// eslint-disable-next-line no-unused-vars
function AgentEditor() {
  const { id } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const { addToast } = useOutletContext()
  const { t } = useTranslation(['agents', 'library', 'chats', 'home'])
  const isEdit = !!id
  const imported = location.state?.importedConfig || null

  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
  // The section the reader is in, for the section nav.
  const [activeSec, setActiveSec] = useState(GROUPS[0].id)
  const [savedConfig, setSavedConfig] = useState(null)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [tryOpen, setTryOpen] = useState(false)
  const [previewTab, setPreviewTab] = useState('config')
  const [form, setForm] = useState(emptyAgent())
  // Snapshot of the form as first loaded, for the unsaved-changes guard.
  const initialFormRef = useRef(null)
  const [collectionNames, setCollectionNames] = useState([])
  // The chat models, for the picker and for the capabilities line under it.
  const { models: chatModels, loading: modelsLoading } = useModels(CAP_CHAT, { keepBroken: true })
  const [collapsed, setCollapsed] = useState(() => new Set())
  const formRef = useRef(null)
  // The choice of whose notes the agent last had, for coming back from Off.
  const lastWhose = useRef('')
  if (form.memory && form.memory !== 'none') lastWhose.current = form.memory

  // Suggestions are best effort: a failure leaves the field free text.
  useEffect(() => {
    agentCollectionsApi.list(false)
      .then(data => setCollectionNames((Array.isArray(data?.collections) ? data.collections : []).map(c => (typeof c === 'string' ? c : c?.name)).filter(Boolean)))
      .catch(() => {})
  }, [])

  useEffect(() => {
    const init = async () => {
      setLoadError(null)
      try {
        // A new agent starts with the default choice of notes. An agent that
        // is read or imported keeps what it has: none means "as before".
        let initialForm = newAgent()
        if (isEdit) {
          const agent = await chatAgentsApi.get(id)
          initialForm = { ...emptyAgent(), ...agent, memory: memoryChoiceOf(agent), memory_save: memorySaveOf(agent), folders: folderRows(agent) }
          setSavedConfig(agent)
        } else if (imported) {
          initialForm = { ...emptyAgent(), ...imported.agent, memory: memoryChoiceOf(imported.agent), memory_save: memorySaveOf(imported.agent), folders: folderRows(imported.agent) }
        }
        initialFormRef.current = initialForm
        setForm(initialForm)
      } catch (err) {
        // An agent that could not be read is said in place of the form: an
        // empty form would save over it.
        setLoadError(err)
      } finally {
        setLoading(false)
      }
    }
    init()
  }, [id, isEdit, imported, attempt])

  const updateField = useCallback((name, value) => setForm(prev => ({ ...prev, [name]: value })), [])

  // The Tools and Skills lists are the ones the Tools menu of a chat draws
  // (SwitchPanel), with the agent's grants and switches applied to them.
  const tools = useAgentTools(form, updateField)
  // The person's own memory switch, read only here: an agent cannot widen it.
  const memoryUser = useMemorySettings()
  const toolGroups = useToolGroups(tools, { commandState: false, forAgent: true })
  // What the server says each connection of this agent lets its senders do.
  const access = useAgentAccess(form.id || '', tools.connections)

  // The definition exactly as it is saved. The preview shows this object and
  // Save sends it.
  // The folders as saved: a save sends `folders` only when the form changed
  // them, so untouched folders are kept, also a missing one.
  const initialFolders = useMemo(() => (isEdit ? folderRows(savedConfig) : []), [isEdit, savedConfig])
  const ab = useAgentAbilities({ form, setForm, updateField, tools, initialFolders })
  const { applied, setApplied, undoApplied, filesChoice } = ab
  const payload = useMemo(() => agentPayload(form, { initialFolders }), [form, initialFolders])
  // A folder row was added or changed and has no folder chosen.
  // With No files the rows do not count: the server ignores them.
  const folderUnchosen = filesChoiceOf(form) !== 'none' && foldersUnchosen(initialFolders, form.folders)

  // Adding the first grant to an agent that had none turns off what it did
  // by default on its messaging connections. Say so, and offer the fix.
  const missingGrants = isEdit ? missingConnectorGrants(savedConfig?.builtins, form.builtins) : []

  // Bring a field into view below the sticky header, then focus it.
  const focusField = (fieldId) => {
    const el = document.getElementById(fieldId)
    if (!el) return
    el.scrollIntoView({ block: 'center' })
    el.focus({ preventScroll: true })
  }

  const handleSubmit = async (e) => {
    e?.preventDefault?.()
    if (!payload.name) {
      addToast(t('create.needsName'), 'warning')
      focusField('ag-name')
      return
    }
    if (folderUnchosen) {
      addToast(t('folder.needsChoice'), 'warning')
      document.querySelector('[data-section="Files"]')?.scrollIntoView({ block: 'start' })
      return
    }
    setSaving(true)
    try {
      if (isEdit) {
        await chatAgentsApi.update(id, payload)
        addToast(t('create.updated', { name: payload.name }), 'success')
      } else {
        await chatAgentsApi.create(payload)
        addToast(t('create.created', { name: payload.name }), 'success')
      }
      initialFormRef.current = form
      navigate('/app/agents')
    } catch (err) {
      addToast(t('create.saveFailed', { message: err.message }), 'error')
    } finally {
      setSaving(false)
    }
  }

  // Mark the section in view in the nav: the last one whose top has passed
  // the line under the sticky header.
  useEffect(() => {
    if (loading || loadError) return undefined
    let frame = 0
    const update = () => {
      frame = 0
      let current = GROUPS[0].id
      // The line under the sticky header (and the nav chips on a phone).
      const bar = parseFloat(formRef.current?.style.getPropertyValue('--aed-bar-h')) || 132
      const line = bar + (window.innerWidth <= 768 ? 72 : 40)
      for (const g of GROUPS) {
        const el = document.getElementById(secDomId(g.id))
        if (el && el.getBoundingClientRect().top <= line) current = g.id
      }
      setActiveSec(current)
    }
    const onScroll = () => { if (!frame) frame = requestAnimationFrame(update) }
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll)
    update()
    return () => {
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
      if (frame) cancelAnimationFrame(frame)
    }
  }, [loading, loadError])

  // The sticky nav sits right under the header, whatever height the header has.
  useEffect(() => {
    const form = formRef.current
    const bar = form?.querySelector('.aed-bar')
    if (!bar) return undefined
    const set = () => form.style.setProperty('--aed-bar-h', `${Math.round(bar.getBoundingClientRect().height)}px`)
    set()
    if (typeof ResizeObserver === 'undefined') return undefined
    const ro = new ResizeObserver(set)
    ro.observe(bar)
    return () => ro.disconnect()
  }, [loading, loadError])

  // An address with #aed-sec-<card> (the Finish setup card sends one) opens on that card.
  const hashTarget = location.hash.startsWith('#aed-sec-') ? location.hash.slice('#aed-sec-'.length) : ''
  useEffect(() => {
    if (!hashTarget || loading || loadError) return
    document.getElementById(secDomId(hashTarget))?.scrollIntoView({ block: 'start' })
  }, [hashTarget, loading, loadError])

  if (loading || loadError) {
    return (
      <Page className="ag-page" data-testid="agent-edit">
        <div className="ag-home">
          <button type="button" className="ag-back" onClick={() => navigate('/app/agents')}><Icon name="arrow-left" /> {t('agent.back')}</button>
          {loadError
            ? <LoadError title={t('load.agentForm')} error={loadError} onRetry={() => { setLoading(true); setAttempt(n => n + 1) }} />
            : <SkeletonRows count={4} />}
        </div>
      </Page>
    )
  }

  const dirty = initialFormRef.current != null &&
    (JSON.stringify(payload) !== JSON.stringify(agentPayload(initialFormRef.current)) || folderUnchosen)

  const leave = () => navigate(isEdit ? `/app/agents/${encodeURIComponent(id)}` : '/app/agents')

  // What each fold says about itself: ready mark, one line, and, when editing,
  // how many of its fields differ from the saved agent.
  const memoryLevel = memoryLevelOf(form)
  const baseline = savedConfig ? agentPayload(savedConfig) : {}
  const diff = diffConfig(baseline, payload)
  const changedKeys = new Set(savedConfig ? diff.map(d => d.key) : [])
  const effective = effectiveLines({
    form,
    rows: flatRows(toolGroups),
    skills: (tools.skills || []).filter(s => s.enabled && !s.gone && s.layer !== 'addon').map(s => ({ on: true, id: s.name, label: s.name })),
    addons: tools.addons,
    skillRows: tools.skills,
    mcpUnknown: ab.unknownMcp,
    memory: memoryUser.settings,
    access,
    isEdit,
  })
  // The add-ons that are on and cannot work: the section says so, and so does
  // the card on the nav.
  const stuckAddons = tools.addons.filter(a => !a.gone && !a.off && addonNow(a).tone === 'warn').length + ab.unknownMcp.length
  const countOf = (n, key) => (n ? t(key, { count: n }) : t('create.noneYet'))

  const foldInfo = (s) => {
    let summary = ''
    let state = 'empty'
    let keys = (SUMMARY_FIELDS[s.id] || []).map(f => f.name)
    if (s.id === 'Notes') {
      keys = ['memory']
      const lv = t(`chats:agentMemory.summary.level.${memoryLevel}`)
      summary = memoryLevel === 'off' ? lv : t('chats:agentMemory.summary.combined', { level: lv, choice: t(`chats:agentMemory.summary.${form.memory || 'asBefore'}`) })
      if (form.memory_save === 'auto' && memoryLevel === 'saves') summary = t('chats:agentMemory.summary.withAuto', { choice: summary })
      state = 'ready'
    }
    else if (s.id === 'MemorySettings') { keys = ['collections']; summary = countOf(form.collections.length, 'create.countCollections'); state = form.collections.length ? 'ready' : 'empty' }
    else if (s.id === 'Skills') { keys = ['skills']; summary = countOf(form.skills.length, 'create.countSkills'); state = form.skills.length ? 'ready' : 'empty' }
    else if (s.id === 'Uses') {
      keys = ['builtins_off', 'builtins_on', 'builtins', 'addons', 'mcp_servers', 'network', 'connections', 'resources']
      const { on, total } = switchCounts(switchRows(toolGroups))
      summary = total ? t('create.toolsSummary', { on, total }) : t('create.noneYet')
      state = on ? 'ready' : 'empty'
      if (stuckAddons > 0) { summary = t('create.uses.needsSummary', { count: stuckAddons }); state = 'needs' }
    }
    else if (s.id === 'Files') { keys = ['folders']; ({ summary, state } = folderFold()) }
    else if (s.id === 'Connected') {
      summary = tools.connections.length ? t('create.connections.summary', { count: tools.connections.length }) : t('create.noneYet')
      state = 'ready'
    }
    else if (s.id === 'Effective') {
      const n = blockedCount(effective)
      summary = n ? t('create.eff.summaryBlocked', { count: n }) : t('create.eff.summaryOk')
      state = n ? 'needs' : 'ready'
    }
    else {
      summary = summarise(SUMMARY_FIELDS[s.id], form)
      state = summary ? 'ready' : 'empty'
    }
    if (s.id === 'BasicInfo' && !payload.name) { state = 'needs'; summary = t('create.needsName') }
    if (s.id === 'ModelSettings' && !payload.model) summary = t('create.anyModel')
    const changed = keys.filter(k => changedKeys.has(k)).length
    return { summary, state, changed }
  }

  // What the Files fold says when closed, and whether it needs the person.
  function folderFold() {
    const rows = form.folders || []
    if (filesChoice === 'none') return { summary: t('folder.none'), state: 'ready' }
    if (!rows.length) return { summary: t('folder.all'), state: 'ready' }
    const open = rows.find(r => !r.path)
    if (open) {
      const st = folderState({ folder: open })
      if (st === 'missing') return { summary: t('folder.summaryMissing'), state: 'needs' }
      if (st === 'unset') return { summary: t('folder.summaryUnset'), state: 'needs' }
      return { summary: t('folder.needsChoice'), state: 'needs' }
    }
    if (rows.length > 1) return { summary: t('folder.summaryMany', { n: rows.length }), state: 'ready' }
    const f = rows[0]
    return { summary: t(f.mode === 'write' ? 'folder.summaryWrite' : 'folder.summaryRead', { path: shownPath(f.path) }), state: 'ready' }
  }
  function shownPath(path) { return path === '/' ? t('folder.home') : path }

  // The level of the tool switches and a template applied by hand are in
  // useAgentAbilities; both stay in the form until Save and can be undone.
  const levelReady = ab.levelReady
  const level = ab.level
  const applyLevel = ab.applyLevel
  // The memory level: it sets the choice and the grant together (see
  // withMemoryLevel), with the same way back as a level. The choice of whose
  // notes the agent had is kept for when it comes back from Off.
  const setMemoryLevel = (lv) => {
    if (lv === memoryLevel) return
    setApplied({ kind: 'level', id: lv, name: t(`chats:agentMemory.level.${lv}.title`), before: snapshot(form, ['memory', 'builtins']) })
    setForm(prev => withMemoryLevel(prev, lv, lastWhose.current))
  }
  // The skills with an "its add-on is off" line, and the add-on's title as the source.
  const skillRowsShown = tools.skills.map(sk => (sk.layer === 'addon' && !sk.gone && !(form.addons || []).includes(sk.addon)
    ? { ...sk, unavailable: t('create.uses.skillAddonOff') } : sk))
  // The skills the host grants with the notes are rows of their own: on while
  // memory is on, off with the reason when it is not. They have no switch to flip.
  const memSkillState = memorySkillsState(form, memoryUser.settings)
  const grantedRowsShown = (tools.grantedSkills || []).map(sk => ({
    ...sk, enabled: memSkillState === 'on', locked: true,
    sourceLabel: memorySkillFrom(t, sk.provider), unavailable: t(`create.uses.memorySkills.${memSkillState}`),
  }))
  const allSkillRowsShown = [...skillRowsShown, ...grantedRowsShown]
  const chosenModel = chatModels.find(m => m.id === form.model)
  const describeModel = (name) => describeChatModel(chatModels, name, c => t(`home:picker.cap.${c}`, { defaultValue: c }))

  const renderSection = (sid) => {
    switch (sid) {
      case 'BasicInfo':
        return (
          <>
            <div className="form-group">
              <label className="form-label" htmlFor="ag-name">{t('create.name')} <span className="text-error">*</span></label>
              <input id="ag-name" className="input" value={form.name} maxLength={80} placeholder="research-assistant" onChange={(e) => updateField('name', e.target.value)} />
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="ag-description">{t('create.description')}</label>
              <input id="ag-description" className="input" value={form.description} maxLength={400} onChange={(e) => updateField('description', e.target.value)} />
              <p className="ag-note">{t('create.descriptionHelp')}</p>
            </div>
          </>
        )
      case 'ModelSettings':
        return (
          <div className="form-group">
            <label className="form-label" htmlFor="agent-model">{t('create.model')}</label>
            <ModelPicker
              id="agent-model"
              labels={{ label: t('create.model') }}
              autoSelect={false}
              rememberSelection={false}
              defaultChoice={t('create.modelPlaceholder')}
              defaultSelected={!form.model}
              onChooseDefault={() => updateField('model', '')}
              value={form.model}
              onChange={(v) => updateField('model', v)}
              capability={CAP_CHAT}
              models={chatModels}
              loading={modelsLoading}
              describe={describeModel}
              variant="field"
              searchable
              placeholder={t('create.modelPlaceholder')}
            />
            {chosenModel && (
              <>
                <p className="aed-caps__lead">{t('create.modelCaps')}</p>
                <ul className="aed-caps" data-testid="agent-model-caps">
                  <li className="aed-caps__chip">{t('create.modelChat')}</li>
                  {modelCaps(chosenModel).filter(c => c !== 'chat').map(c => (
                    <li key={c} className="aed-caps__chip">{c === 'vision' ? t('home:picker.vision') : t(`home:picker.cap.${c}`, { defaultValue: c })}</li>
                  ))}
                </ul>
              </>
            )}
            <p className="ag-note">{t('create.modelHelp')}</p>
          </div>
        )
      case 'PromptsGoals':
        return (
          <div className="form-group">
            <label className="form-label" htmlFor="ag-instructions">{t('create.instructions')}</label>
            <textarea id="ag-instructions" className="textarea" rows={10} value={form.instructions} onChange={(e) => updateField('instructions', e.target.value)} />
            <p className="ag-note">{t('create.instructionsHelp')}</p>
          </div>
        )
      case 'Notes':
        return (
          <AgentMemoryChoice
            level={memoryLevel}
            onLevel={setMemoryLevel}
            value={form.memory || ''}
            onChange={(c) => setForm(prev => withMemoryChoice(prev, c))}
            userOff={memoryUser.settings?.enabled === false}
            saveRule={form.memory_save || ''}
            onSaveRule={(r) => updateField('memory_save', r || 'use_mine')}
            userLevel={memoryUser.settings?.save_mode}
            notice={applied?.kind === 'level' && (
              <AppliedNotice applied={applied} onUndo={undoApplied} onKeep={() => setApplied(null)} testId="agent-memory-applied" />
            )}
          >
            <AgentMemorySkills rows={tools.grantedSkills} state={memorySkillsState(form, memoryUser.settings)} />
          </AgentMemoryChoice>
        )
      case 'MemorySettings':
        return (
          <>
            <p className="ag-note">{t('create.collectionsHelp')}</p>
            <NameList label={t('create.collections')} hint={t('create.collectionsHint')} value={form.collections}
              suggestions={collectionNames} onChange={(v) => updateField('collections', v)} testId="agent-collections" />
          </>
        )
      case 'Skills':
        return (
          <>
            <p className="ag-note">{t('create.skillsHelp')}</p>
            <div className="ag-switches" data-testid="agent-skills-panel">
              <SkillsPanel
 skills={allSkillRowsShown} replaced={tools.replacedSkills} onSet={tools.setSkillsOn} error={tools.skillsError}
                testId="agent-skills" fromLabel heading={t('create.skillsHeading')} later={t('create.skillsLater')}
              />
            </div>
          </>
        )
      case 'Effective':
        return <AgentEffective lines={effective} dirty={dirty} isEdit={isEdit} hasConnections={access.length > 0} onGoTo={goTo} />
      case 'Connected':
        return <AgentConnected bindings={tools.connections} />
      case 'Uses':
        return (
          <AgentUsesPanel
            abilities={ab} tools={tools} toolGroups={toolGroups} form={form} updateField={updateField}
            presets={<AgentPresets active={level} ready={levelReady} onPick={applyLevel} />}
            presetNote={t('create.uses.presetNote')}
            notices={(
              <>
                <AppliedNotice applied={applied?.kind === 'preset' ? applied : null} onUndo={undoApplied} onKeep={() => setApplied(null)} testId="agent-preset-applied" />
                <p className="ag-note">{t('create.toolsHelp')}</p>
                {memoryUser.settings && memoryUser.settings.enabled === false && (
                  <Callout tone="info" icon="eye-off" testId="agent-memory-off">
                    {t('create.memoryOffUser')}{' '}<Link className="cs-link" to="/app/account?tab=memory">{t('create.memoryOffFix')}</Link>
                  </Callout>
                )}
                {missingGrants.length > 0 && (
                  <Callout
                    tone="warn" testId="agent-grants-notice"
                    title={t('create.grantsNoticeTitle')}
                    actions={<Button size="sm" onClick={() => updateField('builtins', missingGrants.reduce((l, g) => setGrant(l, g, true), form.builtins || []))} data-testid="agent-grants-add-missing">{t('create.grantsAddMissing')}</Button>}
                  >
                    {t('create.grantsNotice', { grants: missingGrants.map(g => t(`create.grantNames.${g.replace(/\./g, '_')}`, { defaultValue: g })).join(', ') })}
                  </Callout>
                )}
              </>
            )}
          />
        )
      case 'Files':
        return <AgentFiles form={form} updateField={updateField} abilities={ab} savedConfig={savedConfig} isEdit={isEdit} />
      case 'export':
        return (
          <>
            <p className="ag-note">{t('create.exportHelp')}</p>
            {savedConfig?.folder?.path && <p className="ag-note ag-note--bottom" data-testid="agent-export-folder">{folderRows(savedConfig).length > 1 ? t('folder.exportNoteMany', { n: folderRows(savedConfig).length }) : t('folder.exportNote', { path: shownPath(savedConfig.folder.path) })}</p>}
            <Button icon="download" onClick={() => downloadAgentFile(savedConfig, addToast, t)}>
              {t('create.exportButton')}
            </Button>
          </>
        )
      default:
        return null
    }
  }

  const saveLabel = isEdit ? t('create.save') : imported ? t('create.titleImport') : t('create.titleCreate')
  const headName = (form.name || '').trim()
  // A card with nothing in it (Advanced holds only the export of a saved agent) is not shown.
  const visibleGroups = GROUPS.map(g => ({ ...g, parts: g.parts.filter(pid => pid !== 'export' || (isEdit && savedConfig)) })).filter(g => g.parts.length > 0)

  // A card needs the person when one of its parts does.
  const groupNeeds = (g) => g.parts.some(pid => pid !== 'export' && foldInfo({ id: pid }).state === 'needs')

  const folded = (g) => !!g.collapsible && collapsed.has(g.id)
  const flip = (gid) => setCollapsed(prev => { const n = new Set(prev); if (n.has(gid)) n.delete(gid); else n.add(gid); return n })

  const goTo = (gid) => {
    const el = document.getElementById(secDomId(gid))
    if (!el) return
    if (collapsed.has(gid)) setCollapsed(prev => { const n = new Set(prev); n.delete(gid); return n })
    const calm = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    el.scrollIntoView({ behavior: calm ? 'auto' : 'smooth', block: 'start' })
    setActiveSec(gid)
  }

  const status = dirty
    ? t('create.status.unsaved', { count: Math.max(diff.length, 1) })
    : t(isEdit ? 'create.status.clean' : 'create.status.new')
  const toolsInfo = foldInfo({ id: 'Uses' })
  const memoryInfo = foldInfo({ id: 'Notes' })

  return (
    <Page className="ag-page aed-page" data-testid="agent-edit">
      <UnsavedChangesGuard when={dirty && !saving} />
      <form ref={formRef} onSubmit={handleSubmit} noValidate className="ag-edit aed">
        <button type="button" className="ag-back" onClick={leave}><Icon name="arrow-left" /> {isEdit ? savedConfig?.name : t('agent.back')}</button>
        <header className="aed-bar" data-testid="agent-edit-header">
          <div className="aed-bar__who">
            <span className="aed-avatar" aria-hidden="true">{monogram(headName) || <Icon name="sparkles" />}</span>
            <div className="aed-bar__text">
              <h1 className="aed-bar__name" data-empty={headName ? undefined : ''}>{headName || t('create.header.unnamed')}</h1>
              <p className="aed-bar__sub">{isEdit ? t('create.header.modeEdit') : imported ? t('create.titleImport') : t('create.header.modeCreate')}</p>
              <p className="aed-bar__sub aed-bar__sub--status" aria-hidden="true" data-dirty={dirty || undefined}>{status}</p>
            </div>
          </div>
          <div className="aed-bar__acts">
            <span className="aed-status" role="status" data-dirty={dirty || undefined} data-testid="agent-edit-status">{status}</span>
            <Button variant="ghost" onClick={leave}>{t('create.discard')}</Button>
            <Button icon="eye" onClick={() => { setPreviewTab('config'); setPreviewOpen(true) }} data-testid="agent-preview-open">
              {t('create.preview')}
              {diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
            </Button>
            <Button icon="flask" onClick={() => setTryOpen(true)} data-testid="agent-try-open">{t('create.try.open')}</Button>
            <Button type="submit" variant="primary" icon="save" busy={saving}>{saveLabel}</Button>
            {isEdit && savedConfig && (
              <ActionMenu
                ariaLabel={t('create.more')}
                items={[{ key: 'export', icon: 'download', label: t('create.exportButton'), onClick: () => downloadAgentFile(savedConfig, addToast, t) }]}
              />
            )}
          </div>
          <ul className="aed-chips" aria-label={t('create.header.summary')}>
            <li className="aed-chip"><span className="aed-chip__k">{t('create.sections.ModelSettings')}</span><span className="aed-chip__v">{form.model || t('create.anyModel')}</span></li>
            <li className="aed-chip"><span className="aed-chip__k">{t('create.sections.Uses')}</span><span className="aed-chip__v">{toolsInfo.summary}</span></li>
            <li className="aed-chip"><span className="aed-chip__k">{t('create.sections.Notes')}</span><span className="aed-chip__v">{memoryInfo.summary}</span></li>
          </ul>
        </header>

        <div className="aed-layout">
          <nav className="aed-nav" aria-label={t('create.nav.label')}>
            {visibleGroups.map(g => (
              <a
                key={g.id}
                href={`#${secDomId(g.id)}`}
                className="aed-nav__item"
                aria-current={activeSec === g.id ? 'true' : undefined}
                data-needs={groupNeeds(g) || undefined}
                data-testid={`agent-nav-${g.id}`}
                onClick={(e) => { e.preventDefault(); goTo(g.id) }}
              >
                <span className="aed-nav__dot" aria-hidden="true" />
                {t(`create.nav.${g.id}`)}
              </a>
            ))}
          </nav>

          <div className="aed-main">
            {imported && (
              <div className="ag-import-note" role="note" data-testid="agent-import-note">
                <p>{imported.legacy ? t('create.importLegacy') : t('create.importNew')}</p>
                {imported.dropped.length > 0 && <p>{t('create.importDropped', { keys: imported.dropped.join(', ') })}</p>}
              </div>
            )}

            {visibleGroups.map(g => {
              // What the card says about itself: the first part that needs the
              // person, and how many of its fields differ from the saved agent.
              const infos = g.parts.filter(pid => pid !== 'export').map(pid => foldInfo({ id: pid }))
              const needs = infos.find(i => i.state === 'needs')
              const changed = infos.reduce((n, i) => n + i.changed, 0)
              return (
              <section key={g.id} id={secDomId(g.id)} className="aed-card" aria-labelledby={`${secDomId(g.id)}-h`} data-group={g.id} data-collapsed={folded(g) || undefined}>
                <div className="aed-card__head">
                  <h2 id={`${secDomId(g.id)}-h`} className="aed-card__title">
                    {g.collapsible ? (
                      <button type="button" className="aed-card__toggle" aria-expanded={!folded(g)} aria-controls={`${secDomId(g.id)}-body`} onClick={() => flip(g.id)} data-testid={`agent-toggle-${g.id}`}>
                        {t(`create.nav.${g.id}`)}<Icon name="chevron-down" className="aed-card__chev" />
                      </button>
                    ) : t(`create.nav.${g.id}`)}
                  </h2>
                  <p className="aed-card__lead">{t(`create.lead.${g.id}`)}</p>
                  {needs && <span className="aed-card__needs" data-testid="agent-card-needs">{needs.summary}</span>}
                  {changed > 0 && <span className="ag-badge-changed">{t('create.changed', { count: changed })}</span>}
                </div>
                <div className="aed-card__body" id={`${secDomId(g.id)}-body`} hidden={folded(g)}>
                {g.parts.map(pid => {
                  const info = pid === 'export' ? null : foldInfo({ id: pid })
                  return (
                    <div key={pid} className="aed-part" data-section={pid} data-bare={g.bare || undefined}>
                      {!g.bare && (
                        <div className="aed-part__head">
                          {info && <span className="ag-ready" data-state={info.state} aria-hidden="true"><Icon name={info.state === 'needs' ? 'warning' : 'check'} /></span>}
                          <h3 className="aed-part__title">{t(`create.sections.${pid}`)}</h3>
                          {info && <span className="aed-part__sum">{info.summary}</span>}
                          {info && info.changed > 0 && <span className="ag-badge-changed">{t('create.changed', { count: info.changed })}</span>}
                        </div>
                      )}
                      <div className="aed-part__body" data-pair={pid === 'BasicInfo' || undefined}>
                        {renderSection(pid)}
                      </div>
                    </div>
                  )
                })}
                </div>
              </section>
              )
            })}
          </div>
        </div>
      </form>

      {tryOpen && <AgentTryIt form={form} tools={tools} models={chatModels} onClose={() => setTryOpen(false)} />}

      {previewOpen && (
        <PreviewSheet
          tab={previewTab}
          onTab={setPreviewTab}
          payload={payload}
          diff={diff}
          isEdit={isEdit}
          saving={saving}
          onSave={() => { setPreviewOpen(false); handleSubmit() }}
          onClose={() => setPreviewOpen(false)}
        />
      )}
    </Page>
  )
}

// The config as it will be saved, and what differs from the saved agent.
// Secret values are hidden here; they are saved as typed.
// eslint-disable-next-line no-unused-vars
function PreviewSheet({ tab, onTab, payload, diff, isEdit, saving, onSave, onClose }) {
  const { t } = useTranslation('agents')
  const { sheetRef, closeRef } = useSheetFocus(onClose)

  const shown = useCallback((v) => displayValue(maskSecrets(v)), [])
  const json = useMemo(() => JSON.stringify(maskSecrets(payload), null, 2), [payload])

  return createPortal(
    <div className="dk-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={sheetRef}
        className="dk-sheet dk-sheet--wide"
        role="dialog"
        aria-modal="true"
        aria-labelledby="agent-preview-title"
        data-state="open"
        data-testid="agent-preview"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="agent-preview-title">{t('create.previewTitle')}</h2>
            <p className="dk-sheet-desc">{isEdit ? t('create.previewEdit') : t('create.previewNew')}</p>
          </div>
          <button ref={closeRef} type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('create.close')} onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        <div className="dk-sheet-body">
          <div className="dk-tabs ag-sheet-tabs" role="tablist" aria-label={t('create.previewTitle')}>
            <button type="button" role="tab" className="dk-tab" aria-selected={tab === 'config'} onClick={() => onTab('config')}>{t('create.tabConfig')}</button>
            <button type="button" role="tab" className="dk-tab" aria-selected={tab === 'changes'} onClick={() => onTab('changes')}>
              {t('create.tabChanges')}{diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
            </button>
          </div>
          {tab === 'config' ? (
            <div className="dk-tabpanel" role="tabpanel" data-testid="agent-preview-config">
              <p className="ag-note ag-note--bottom">{t('create.configNote')}</p>
              <pre className="ag-pre">{json}</pre>
            </div>
          ) : (
            <div className="dk-tabpanel" role="tabpanel" data-testid="agent-preview-changes">
              {diff.length === 0 ? (
                <p className="ag-note">{isEdit ? t('create.noChanges') : t('create.nothingSet')}</p>
              ) : (
                <>
                  {!isEdit && <p className="ag-note ag-note--bottom">{t('create.allNew')}</p>}
                  <div className="ag-diff">
                    {diff.map(d => (
                      <div key={d.key} className="ag-diff__item" data-key={d.key}>
                        <span className="ag-diff__key">{d.key}</span>
                        <div className="ag-diff__pair">
                          <div className="ag-diff__side" data-side="before"><span className="ag-diff__label">{isEdit ? t('create.saved') : t('create.default')}</span>{shown(d.before) || t('create.empty')}</div>
                          <div className="ag-diff__side" data-side="after"><span className="ag-diff__label">{t('create.new')}</span>{shown(d.after) || t('create.empty')}</div>
                        </div>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>
          )}
        </div>
        <div className="dk-sheet-foot">
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('create.close')}</button>
          <button type="button" className="dk-btn dk-btn--primary" disabled={saving} onClick={onSave}>
            {isEdit ? t('create.save') : t('create.titleCreate')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
