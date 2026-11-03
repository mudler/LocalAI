import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useNavigate, useLocation, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentCollectionsApi, chatAgentsApi, modelMCPServersApi } from '../utils/api'
import { useAgentTools } from '../hooks/useAgentTools'
import { useToolGroups } from '../hooks/useToolGroups'
import { AGENT_TEMPLATES, diffConfig, displayValue, maskSecrets, summarise } from '../utils/agentConfigTools'
import { agentPayload, effectiveModes, emptyAgent, folderRows, folderState, foldersUnchosen, MAX_FOLDERS, missingConnectorGrants, setGrant } from '../utils/chatAgents'
import { downloadAgentFile } from '../utils/agentFiles'
import { joinAgentMCP, splitAgentMCP } from '../utils/chatMCP'
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
import { Button, Callout, LoadError, Page, PageHeader, Segmented, SkeletonRows } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import AgentFolderDialog from '../components/agents/AgentFolderDialog'
// eslint-disable-next-line no-unused-vars
import { SwitchPanel } from '../components/chat/SwitchMenu'
import { switchCounts } from '../utils/chatSwitchRows'
// eslint-disable-next-line no-unused-vars
import { SkillsPanel } from '../components/chat/SkillsMenu'

// The sections of the form. Each folds; Basic info is open at first.
const SECTIONS = [
  { id: 'BasicInfo', icon: 'info' },
  { id: 'ModelSettings', icon: 'brain' },
  { id: 'PromptsGoals', icon: 'target' },
  { id: 'Files', icon: 'folder' },
  { id: 'MemorySettings', icon: 'database' },
  { id: 'Skills', icon: 'sparkles' },
  { id: 'Tools', icon: 'plug' },
  { id: 'Grants', icon: 'shield' },
  { id: 'MCP', icon: 'server' },
]

// The fields each fold summarises on its closed line.
const SUMMARY_FIELDS = {
  BasicInfo: [{ name: 'name', type: 'text' }, { name: 'description', type: 'textarea' }],
  ModelSettings: [{ name: 'model', type: 'text' }],
  PromptsGoals: [{ name: 'instructions', type: 'textarea' }],
}

// --- Main component ---

export default function AgentCreate() {
  const { id } = useParams()
  const navigate = useNavigate()
  const location = useLocation()
  const { addToast } = useOutletContext()
  const { t } = useTranslation(['agents', 'library'])
  const [searchParams] = useSearchParams()
  const templateId = searchParams.get('template') || ''
  const isEdit = !!id
  const imported = location.state?.importedConfig || null

  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(null)
  const [attempt, setAttempt] = useState(0)
  const [saving, setSaving] = useState(false)
  // Sections fold. Basics is open at first; the rest open on demand.
  const [openSections, setOpenSections] = useState(() => new Set(['BasicInfo']))
  const [savedConfig, setSavedConfig] = useState(null)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [previewTab, setPreviewTab] = useState('config')
  const [form, setForm] = useState(emptyAgent())
  // null, a row index, or 'new' for a row being added
  const [choosingFolder, setChoosingFolder] = useState(null)
  // Snapshot of the form as first loaded, for the unsaved-changes guard.
  const initialFormRef = useRef(null)
  const [collectionNames, setCollectionNames] = useState([])
  const [modelServers, setModelServers] = useState([])
  const [grantList, setGrantList] = useState([])

  // Suggestions are best effort: a failure leaves the field free text.
  useEffect(() => {
    agentCollectionsApi.list(false)
      .then(data => setCollectionNames((Array.isArray(data?.collections) ? data.collections : []).map(c => (typeof c === 'string' ? c : c?.name)).filter(Boolean)))
      .catch(() => {})
  }, [])

  // The grants the editor can list: best effort, an empty list hides the checklist.
  useEffect(() => {
    chatAgentsApi.builtinGrants()
      .then(r => setGrantList(Array.isArray(r?.grants) ? r.grants : []))
      .catch(() => {})
  }, [])

  // The servers of the fixed model are suggestions; with no fixed model the field is free text.
  const fixedModel = form.model || ''
  useEffect(() => {
    if (!fixedModel) { setModelServers([]); return undefined }
    let live = true
    modelMCPServersApi.list(fixedModel).then(r => { if (live) setModelServers((r.servers || []).map(s => s.name).filter(Boolean)) }).catch(() => { if (live) setModelServers([]) })
    return () => { live = false }
  }, [fixedModel])

  useEffect(() => {
    const init = async () => {
      setLoadError(null)
      try {
        let initialForm = emptyAgent()
        if (isEdit) {
          const agent = await chatAgentsApi.get(id)
          initialForm = { ...initialForm, ...agent, folders: folderRows(agent) }
          setSavedConfig(agent)
        } else if (imported) {
          initialForm = { ...initialForm, ...imported.agent, folders: folderRows(imported.agent) }
        } else if (templateId) {
          // A starting point fills the form; nothing is saved until the person saves.
          const tpl = AGENT_TEMPLATES.find(x => x.id === templateId)
          if (tpl) initialForm = { ...initialForm, name: tpl.name, description: tpl.description, instructions: tpl.instructions }
        }
        initialFormRef.current = initialForm
        setForm(initialForm)
        if (imported) setOpenSections(new Set(SECTIONS.map(s => s.id)))
      } catch (err) {
        // An agent that could not be read is said in place of the form: an
        // empty form would save over it.
        setLoadError(err)
      } finally {
        setLoading(false)
      }
    }
    init()
  }, [id, isEdit, imported, templateId, attempt])

  const updateField = useCallback((name, value) => setForm(prev => ({ ...prev, [name]: value })), [])

  // The Tools and Skills lists are the ones the Tools menu of a chat draws
  // (SwitchPanel), with the agent's grants and switches applied to them.
  const tools = useAgentTools(form, updateField)
  const toolGroups = useToolGroups(tools, { commandState: false, forAgent: true })

  // The definition exactly as it is saved. The preview shows this object and
  // Save sends it.
  // The folders as saved: a save sends `folders` only when the form changed
  // them, so untouched folders are kept, also a missing one.
  const initialFolders = useMemo(() => (isEdit ? folderRows(savedConfig) : []), [isEdit, savedConfig])
  const payload = useMemo(() => agentPayload(form, { initialFolders }), [form, initialFolders])
  // A folder row was added or changed and has no folder chosen.
  const folderUnchosen = foldersUnchosen(initialFolders, form.folders)

  // Adding the first grant to an agent that had none turns off what it did
  // by default on its messaging connections. Say so, and offer the fix.
  const missingGrants = isEdit ? missingConnectorGrants(savedConfig?.builtins, form.builtins) : []

  const handleSubmit = async (e) => {
    e?.preventDefault?.()
    if (!payload.name) {
      addToast(t('create.needsName'), 'warning')
      setOpenSections(prev => new Set([...prev, 'BasicInfo']))
      return
    }
    if (folderUnchosen) {
      addToast(t('folder.needsChoice'), 'warning')
      setOpenSections(prev => new Set([...prev, 'Files']))
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

  const mcp = splitAgentMCP(form.mcp_servers)
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
  const baseline = savedConfig ? agentPayload(savedConfig) : {}
  const diff = diffConfig(baseline, payload)
  const changedKeys = new Set(savedConfig ? diff.map(d => d.key) : [])
  const countOf = (n, key) => (n ? t(key, { count: n }) : t('create.noneYet'))

  const foldInfo = (s) => {
    let summary = ''
    let state = 'empty'
    let keys = (SUMMARY_FIELDS[s.id] || []).map(f => f.name)
    if (s.id === 'MemorySettings') { keys = ['collections']; summary = countOf(form.collections.length, 'create.countCollections'); state = form.collections.length ? 'ready' : 'empty' }
    else if (s.id === 'Skills') { keys = ['skills']; summary = countOf(form.skills.length, 'create.countSkills'); state = form.skills.length ? 'ready' : 'empty' }
    else if (s.id === 'Tools') {
      keys = ['builtins_off', 'builtins_on', 'addons', 'mcp_servers', 'network', 'connections']
      const { on, total } = switchCounts(toolGroups.flatMap(g => g.rows))
      summary = total ? t('create.toolsSummary', { on, total }) : t('create.noneYet')
      state = on ? 'ready' : 'empty'
    }
    else if (s.id === 'Grants') {
      keys = ['builtins']
      const n = (form.builtins || []).length
      summary = n ? t('create.countGrants', { count: n }) : t('create.grantsDefault')
      state = missingGrants.length ? 'needs' : 'ready'
    }
    else if (s.id === 'Files') { keys = ['folders']; ({ summary, state } = folderFold()) }
    else if (s.id === 'MCP') { keys = ['mcp_servers']; summary = countOf(form.mcp_servers.length, 'create.countServers'); state = form.mcp_servers.length ? 'ready' : 'empty' }
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

  // All my files, or chosen folders: back to the saved rows when there are some.
  const setScope = (scope) => {
    if (scope === 'all') updateField('folders', [])
    else if (!(form.folders || []).length) updateField('folders', initialFolders.length ? initialFolders : [{ mode: 'read' }])
  }
  const setRow = (i, patch) => updateField('folders', form.folders.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const removeRow = (i) => updateField('folders', form.folders.filter((_, j) => j !== i))
  const addRow = () => { updateField('folders', [...form.folders, { mode: 'read' }]); setChoosingFolder('new') }

  const toggleSection = (sid) => setOpenSections(prev => {
    const next = new Set(prev)
    if (next.has(sid)) next.delete(sid)
    else next.add(sid)
    return next
  })

  const applyTemplate = (tpl) => {
    setForm(prev => ({ ...prev, name: tpl.name, description: tpl.description, instructions: tpl.instructions }))
    setOpenSections(prev => new Set([...prev, 'BasicInfo', 'PromptsGoals']))
  }

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
              placeholder={t('create.modelPlaceholder')}
            />
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
                skills={tools.skills} replaced={tools.replacedSkills} onSet={tools.setSkillsOn} error={tools.skillsError}
                testId="agent-skills" heading={t('create.skillsHeading')} later={t('create.skillsLater')}
              />
            </div>
          </>
        )
      case 'Tools':
        return (
          <>
            <p className="ag-note">{t('create.toolsHelp')}</p>
            <div className="ag-switches" data-testid="agent-tools-panel">
              <SwitchPanel
                heading={t('create.toolsHeading')} groups={toolGroups} error={tools.error} testId="agent-tools"
                footer={<Button size="sm" variant="ghost" iconEnd="arrow-right" to="/app/mcp-connectors" data-testid="agent-tools-manage">{t('create.manageConnectors')}</Button>}
              />
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="ag-network">{t('create.network')}</label>
              <select id="ag-network" className="input" value={form.network || 'off'} onChange={(e) => updateField('network', e.target.value)} data-testid="agent-network">
                {['off', 'registries', 'all'].map(p => <option key={p} value={p}>{t(`create.networkPresets.${p}`)}</option>)}
              </select>
              <p className="ag-note">{t('create.networkHelp')}</p>
            </div>
          </>
        )
      case 'Grants':
        return (
          <>
            <p className="ag-note">{t('create.grantsHelp')}</p>
            {missingGrants.length > 0 && (
              <Callout
                tone="warn" testId="agent-grants-notice"
                title={t('create.grantsNoticeTitle')}
                actions={<Button size="sm" onClick={() => updateField('builtins', missingGrants.reduce((l, g) => setGrant(l, g, true), form.builtins || []))} data-testid="agent-grants-add-missing">{t('create.grantsAddMissing')}</Button>}
              >
                {t('create.grantsNotice', { grants: missingGrants.join(', ') })}
              </Callout>
            )}
            <div className="ag-grants" data-testid="agent-grants">
              {grantList.map(g => (
                <label key={g.name} className="ag-grants__row" data-testid="agent-grant">
                  <input type="checkbox" checked={(form.builtins || []).includes(g.name)} onChange={(e) => updateField('builtins', setGrant(form.builtins, g.name, e.target.checked))} />
                  <span className="ag-grants__text">
                    <span className="ag-grants__title">{g.title}{g.sensitive && <span className="ag-grants__tag">{t('create.grantSensitive')}</span>}</span>
                    <span className="ag-note">{g.description}</span>
                  </span>
                </label>
              ))}
            </div>
          </>
        )
      case 'Files': {
        const rows = form.folders || []
        const effective = effectiveModes(rows)
        return (
          <>
            <Segmented
              label={t('folder.scopeLabel')} testId="agent-folder-scope" itemTestId="agent-folder-scope-"
              value={rows.length ? 'one' : 'all'} onChange={setScope}
              options={[{ id: 'all', label: t('folder.all') }, { id: 'one', label: t('folder.one') }]}
            />
            {rows.length > 0 && (
              <div className="ag-folder" data-testid="agent-folder">
                {rows.map((f, i) => {
                  const st = f.path ? 'ok' : folderState({ folder: f })
                  const forced = f.path && effective[i] !== (f.mode === 'write' ? 'write' : 'read')
                  return (
                    <div className="ag-folder__item" key={i} data-testid="agent-folder-row">
                      {f.path ? (
                        <div className="ag-folder__row">
                          <span className="ag-folder__path" data-testid="agent-folder-path"><Icon name="folder" />{shownPath(f.path)}</span>
                          <Button size="sm" onClick={() => setChoosingFolder(i)} data-testid="agent-folder-change">{t('folder.change')}</Button>
                          <Button size="sm" variant="ghost" icon="close" onClick={() => removeRow(i)} aria-label={t('folder.remove', { path: shownPath(f.path) })} data-testid="agent-folder-remove" />
                        </div>
                      ) : st === 'missing' || st === 'unset' ? (
                        <Callout
                          tone="error" role="alert" testId="agent-folder-missing"
                          actions={(
                            <>
                              <Button size="sm" icon="folder" onClick={() => setChoosingFolder(i)}>{t('folder.chooseOther')}</Button>
                              <Button size="sm" variant="ghost" onClick={() => removeRow(i)} data-testid="agent-folder-remove">{t('folder.removeRow')}</Button>
                            </>
                          )}
                        >
                          {t(st === 'missing' ? 'folder.missing' : 'folder.unset')}
                        </Callout>
                      ) : (
                        <div className="ag-folder__row">
                          <Button size="sm" icon="folder" onClick={() => setChoosingFolder(i)} data-testid="agent-folder-choose">{t('folder.choose')}</Button>
                          <Button size="sm" variant="ghost" icon="close" onClick={() => removeRow(i)} aria-label={t('folder.removeRow')} data-testid="agent-folder-remove" />
                        </div>
                      )}
                      <div className="form-group">
                        <span className="form-label">{t('folder.modeLabel')}</span>
                        <Segmented
                          label={t('folder.modeLabel')} testId={`agent-folder-mode-${i}`} itemTestId={`agent-folder-mode-${i}-`}
                          value={f.mode === 'write' ? 'write' : 'read'} onChange={(mode) => setRow(i, { mode })}
                          options={[{ id: 'read', label: t('folder.read') }, { id: 'write', label: t('folder.write') }]}
                        />
                        {forced && <p className="ag-note" data-testid="agent-folder-effective">{t(effective[i] === 'write' ? 'folder.effectiveWrite' : 'folder.effectiveRead')}</p>}
                      </div>
                    </div>
                  )
                })}
                <div className="ag-folder__row">
                  <Button size="sm" icon="plus" onClick={addRow} disabled={rows.length >= MAX_FOLDERS} data-testid="agent-folder-add">{t('folder.add')}</Button>
                  <span className="ag-note" data-testid="agent-folder-count">{t('folder.count', { n: rows.length, max: MAX_FOLDERS })}</span>
                </div>
                {savedConfig?.folder_notice?.not_mounted > 0 && (
                  <p className="ag-note" data-testid="agent-folder-notice">{t('folder.notMounted', { n: savedConfig.folder_notice.not_mounted })}</p>
                )}
                <p className="ag-note">{t('folder.help')}</p>
                <p className="ag-note">{t('folder.commands')}</p>
                {isEdit && <p className="ag-note">{t('folder.nextMessage')}</p>}
              </div>
            )}
            {!rows.length && <p className="ag-note ag-note--gap">{t('folder.allHelp')}</p>}
          </>
        )
      }
      case 'MCP':
        return (
          <>
            <p className="ag-note">{t('create.mcpHelp')}</p>
            <NameList label={t('create.mcpServers')} hint={t('create.mcpHint')} value={mcp.servers} suggestions={modelServers}
              onChange={(v) => updateField('mcp_servers', joinAgentMCP(v, mcp.connectors))} testId="agent-mcp" />
          </>
        )
      default:
        return null
    }
  }

  const title = isEdit ? t('create.titleEdit', { name: savedConfig?.name || '' }) : imported ? t('create.titleImport') : t('create.titleCreate')
  const saveLabel = isEdit ? t('create.save') : imported ? t('create.titleImport') : t('create.titleCreate')

  return (
    <Page className="ag-page" data-testid="agent-edit">
      <UnsavedChangesGuard when={dirty && !saving} />
      <form onSubmit={handleSubmit} noValidate className="ag-edit">
        <div className="ag-edit__head">
          <button type="button" className="ag-back" onClick={leave}><Icon name="arrow-left" /> {isEdit ? savedConfig?.name : t('agent.back')}</button>
          <PageHeader
            title={title}
            supporting={isEdit ? t('create.subEdit') : t('create.subCreate')}
            actions={(
              <div className="ag-edit__acts">
                <Button variant="ghost" onClick={leave}>{t('create.discard')}</Button>
                <Button icon="eye" onClick={() => { setPreviewTab('config'); setPreviewOpen(true) }} data-testid="agent-preview-open">
                  {t('create.preview')}
                  {diff.length > 0 && <span className="dk-badge dk-badge--count">{diff.length}</span>}
                </Button>
                <Button type="submit" variant="primary" icon="save" busy={saving}>{saveLabel}</Button>
              </div>
            )}
          />
        </div>

        {imported && (
          <div className="ag-import-note" role="note" data-testid="agent-import-note">
            <p>{imported.legacy ? t('create.importLegacy') : t('create.importNew')}</p>
            {imported.dropped.length > 0 && <p>{t('create.importDropped', { keys: imported.dropped.join(', ') })}</p>}
          </div>
        )}

        {!isEdit && !imported && (
          <section className="ag-start" aria-labelledby="ag-start-h" data-testid="agent-start">
            <h2 id="ag-start-h" className="ag-h3">{t('create.startFrom')}</h2>
            <div className="ag-start__row">
              {AGENT_TEMPLATES.map(tpl => (
                <button key={tpl.id} type="button" className="dk-chip" onClick={() => applyTemplate(tpl)} data-template={tpl.id}>
                  {t(`templates.${tpl.id}.label`)}
                </button>
              ))}
            </div>
          </section>
        )}

        <div className="ag-folds">
          {SECTIONS.map(s => {
            const open = openSections.has(s.id)
            const info = foldInfo(s)
            return (
              <section key={s.id} className="ag-fold" data-open={open || undefined} data-section={s.id}>
                <h2 className="ag-fold__h">
                  <button
                    type="button"
                    className="ag-fold__button"
                    aria-expanded={open}
                    aria-controls={`ag-fold-${s.id}`}
                    onClick={() => toggleSection(s.id)}
                  >
                    <span className="ag-ready" data-state={info.state} aria-hidden="true"><Icon name={info.state === 'needs' ? 'warning' : 'check'} /></span>
                    <span className="ag-fold__title">{t(`create.sections.${s.id}`)}</span>
                    <span className="ag-fold__sum">{info.summary}</span>
                    {info.changed > 0 ? <span className="ag-badge-changed">{t('create.changed', { count: info.changed })}</span> : <span />}
                    <Icon name="chevron-down" className="ag-fold__chev" />
                  </button>
                </h2>
                {open && (
                  <div className="ag-fold__body" id={`ag-fold-${s.id}`}>
                    {renderSection(s.id)}
                  </div>
                )}
              </section>
            )
          })}
          {isEdit && savedConfig && (
            <section className="ag-fold" data-open={openSections.has('export') || undefined} data-section="export">
              <h2 className="ag-fold__h">
                <button type="button" className="ag-fold__button" aria-expanded={openSections.has('export')} aria-controls="ag-fold-export" onClick={() => toggleSection('export')}>
                  <span className="ag-ready" data-state="ready" aria-hidden="true"><Icon name="check" /></span>
                  <span className="ag-fold__title">{t('create.sections.export')}</span>
                  <span className="ag-fold__sum">{t('create.exportSummary')}</span>
                  <span />
                  <Icon name="chevron-down" className="ag-fold__chev" />
                </button>
              </h2>
              {openSections.has('export') && (
                <div className="ag-fold__body" id="ag-fold-export">
                  <p className="ag-note">{t('create.exportHelp')}</p>
                  {savedConfig.folder?.path && <p className="ag-note ag-note--bottom" data-testid="agent-export-folder">{folderRows(savedConfig).length > 1 ? t('folder.exportNoteMany', { n: folderRows(savedConfig).length }) : t('folder.exportNote', { path: shownPath(savedConfig.folder.path) })}</p>}
                  <Button icon="download" onClick={() => downloadAgentFile(savedConfig, addToast, t)}>
                    {t('create.exportButton')}
                  </Button>
                </div>
              )}
            </section>
          )}
        </div>

        <div className="ag-foot">
          <Button variant="ghost" onClick={leave}>{t('create.discard')}</Button>
          <Button type="submit" variant="primary" icon="save" busy={saving}>{saveLabel}</Button>
        </div>
      </form>

      {choosingFolder !== null && (
        <AgentFolderDialog
          start={(form.folders || [])[choosingFolder === 'new' ? form.folders.length - 1 : choosingFolder]?.path || '/'}
          onCancel={() => {
            // A row that was just added and never chosen goes away again.
            if (choosingFolder === 'new') removeRow(form.folders.length - 1)
            setChoosingFolder(null)
          }}
          onChoose={(path) => {
            const at = choosingFolder === 'new' ? form.folders.length - 1 : choosingFolder
            setChoosingFolder(null)
            setRow(at, { path, state: 'ok' })
          }}
        />
      )}

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
  const sheetRef = useRef(null)
  const closeRef = useRef(null)
  const openerRef = useRef(null)
  // The parent passes a fresh onClose on every render. Read it through a ref
  // so the effect below runs once per open: re-running it would move focus to
  // the opener and back to Close, pulling it out of the control in use.
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose })

  useEffect(() => {
    openerRef.current = document.activeElement
    closeRef.current?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCloseRef.current(); return }
      if (e.key !== 'Tab' || !sheetRef.current) return
      const focusable = Array.from(sheetRef.current.querySelectorAll('button:not([disabled]), [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      const el = openerRef.current
      if (el && document.contains(el)) el.focus?.()
    }
  }, [])

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
