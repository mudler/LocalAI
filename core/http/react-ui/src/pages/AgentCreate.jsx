import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useNavigate, useLocation, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentCollectionsApi, chatAgentsApi, chatMCPConnectorsApi, chatSkillsApi, modelMCPServersApi } from '../utils/api'
import { useLibraryFacts } from '../hooks/useLibraryFacts'
import { useAddonToolAddons } from '../addons/useAddonRegistry'
import { AGENT_TEMPLATES, diffConfig, displayValue, exportAgent, maskSecrets, summarise } from '../utils/agentConfigTools'
import { addEntry, agentPayload, emptyAgent, removeEntry } from '../utils/chatAgents'
import { joinAgentMCP, splitAgentMCP } from '../utils/chatMCP'
import { downloadJSON } from '../utils/download'
import './agents.css'
import './library.css'
// eslint-disable-next-line no-unused-vars
import SearchableModelSelect from '../components/SearchableModelSelect'
// eslint-disable-next-line no-unused-vars
import NameList from '../components/ChatNameList'
// eslint-disable-next-line no-unused-vars
import UnsavedChangesGuard from '../components/UnsavedChangesGuard'
import { CAP_CHAT } from '../utils/capabilities'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { Button, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'

// The sections of the form. Each folds; Basic info is open at first.
const SECTIONS = [
  { id: 'BasicInfo', icon: 'info' },
  { id: 'ModelSettings', icon: 'brain' },
  { id: 'PromptsGoals', icon: 'target' },
  { id: 'MemorySettings', icon: 'database' },
  { id: 'Skills', icon: 'sparkles' },
  { id: 'Addons', icon: 'puzzle' },
  { id: 'MCP', icon: 'server' },
]

// The fields each fold summarises on its closed line.
const SUMMARY_FIELDS = {
  BasicInfo: [{ name: 'name', type: 'text' }, { name: 'description', type: 'textarea' }],
  ModelSettings: [{ name: 'model', type: 'text' }],
  PromptsGoals: [{ name: 'instructions', type: 'textarea' }],
}

// One checkbox per name the person may grant. A granted name that the server
// no longer lists stays visible, marked, so it can be taken away.
// eslint-disable-next-line no-unused-vars
function GrantList({ testId, options, value, onChange, emptyText, goneLabel, hint }) {
  const all = [...options, ...value.filter(v => !options.some(o => o.id === v)).map(v => ({ id: v, name: v, gone: true }))]
  if (all.length === 0) return <p className="ag-note">{emptyText}</p>
  return (
    <div className="ag-grants" data-testid={testId}>
      {all.map(o => (
        <label key={o.id} className="ag-grant" data-on={value.includes(o.id) || undefined}>
          <input
            type="checkbox"
            checked={value.includes(o.id)}
            onChange={(e) => onChange(e.target.checked ? addEntry(value, o.id) : removeEntry(value, o.id))}
          />
          <span>
            <span className="ag-grant__name">{o.name}{o.gone ? ` (${goneLabel})` : ''}</span>
            {(o.hint || hint?.(o)) && <span className="ag-grant__hint">{o.hint || hint(o)}</span>}
          </span>
        </label>
      ))}
    </div>
  )
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
  // Snapshot of the form as first loaded, for the unsaved-changes guard.
  const initialFormRef = useRef(null)
  const [skills, setSkills] = useState([])
  const [collectionNames, setCollectionNames] = useState([])
  const [connectors, setConnectors] = useState([])
  const [modelServers, setModelServers] = useState([])
  const addonList = useAddonToolAddons()
  // Who else uses a skill, so the picker can say "not used yet".
  const library = useLibraryFacts()

  // Suggestions are best effort: a failure leaves the field free text.
  useEffect(() => {
    chatSkillsApi.list().then(r => setSkills(r.skills || [])).catch(() => {})
    chatMCPConnectorsApi.list().then(r => setConnectors(r.connectors || [])).catch(() => {})
    agentCollectionsApi.list(false)
      .then(data => setCollectionNames((Array.isArray(data?.collections) ? data.collections : []).map(c => (typeof c === 'string' ? c : c?.name)).filter(Boolean)))
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
          initialForm = { ...initialForm, ...agent }
          setSavedConfig(agent)
        } else if (imported) {
          initialForm = { ...initialForm, ...imported.agent }
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

  const updateField = (name, value) => setForm(prev => ({ ...prev, [name]: value }))

  // The definition exactly as it is saved. The preview shows this object and
  // Save sends it.
  const payload = useMemo(() => agentPayload(form), [form])

  const handleSubmit = async (e) => {
    e?.preventDefault?.()
    if (!payload.name) {
      addToast(t('create.needsName'), 'warning')
      setOpenSections(prev => new Set([...prev, 'BasicInfo']))
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
  const connectorOptions = connectors.map(c => ({ id: c.id, name: c.name, hint: c.enabled ? '' : t('create.connectorOff') }))
  const skillOptions = skills.filter(s => !s.invalid).reduce((all, s) => (all.some(x => x.id === s.name) ? all : [...all, { id: s.name, name: s.name, hint: s.description, layer: s.layer }]), [])
  const addonOptions = addonList.map(a => ({ id: a.id, name: a.id }))
  const skillUse = (o) => {
    if (library.status !== 'ready') return ''
    const others = library.agents.filter(a => a.id !== id && (a.config?.skills || []).includes(o.id))
    return others.length === 0 ? t('library:usedBy.none') : t('library:usedBy.line', { name: others.map(x => x.name).join(', ') })
  }

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
    JSON.stringify(payload) !== JSON.stringify(agentPayload(initialFormRef.current))

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
    else if (s.id === 'Addons') { keys = ['addons']; summary = countOf(form.addons.length, 'create.countAddons'); state = form.addons.length ? 'ready' : 'empty' }
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
            <label className="form-label">{t('create.model')}</label>
            <SearchableModelSelect
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
            <GrantList testId="agent-skills" options={skillOptions} value={form.skills} onChange={(v) => updateField('skills', v)}
              emptyText={t('create.noSkills')} goneLabel={t('create.gone')} hint={skillUse} />
            <p className="ag-note"><Link to="/app/skills">{t('create.manageSkills')}</Link></p>
          </>
        )
      case 'Addons':
        return (
          <>
            <p className="ag-note">{t('create.addonsHelp')}</p>
            <GrantList testId="agent-addons" options={addonOptions} value={form.addons} onChange={(v) => updateField('addons', v)}
              emptyText={t('create.noAddons')} goneLabel={t('create.gone')} />
            <div className="form-group">
              <label className="form-label" htmlFor="ag-network">{t('create.network')}</label>
              <select id="ag-network" className="input" value={form.network || 'off'} onChange={(e) => updateField('network', e.target.value)} data-testid="agent-network">
                {['off', 'registries', 'all'].map(p => <option key={p} value={p}>{t(`create.networkPresets.${p}`)}</option>)}
              </select>
              <p className="ag-note">{t('create.networkHelp')}</p>
            </div>
          </>
        )
      case 'MCP':
        return (
          <>
            <p className="ag-note">{t('create.mcpHelp')}</p>
            <NameList label={t('create.mcpServers')} hint={t('create.mcpHint')} value={mcp.servers} suggestions={modelServers}
              onChange={(v) => updateField('mcp_servers', joinAgentMCP(v, mcp.connectors))} testId="agent-mcp" />
            <h4 className="agent-subsection-title">{t('create.mcpConnectors')}</h4>
            <GrantList testId="agent-connectors" options={connectorOptions} value={mcp.connectors}
              onChange={(v) => updateField('mcp_servers', joinAgentMCP(mcp.servers, v))}
              emptyText={t('create.noConnectors')} goneLabel={t('create.gone')} />
            <p className="ag-note"><Link to="/app/mcp-connectors">{t('create.manageConnectors')}</Link></p>
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
                  <Button icon="download" onClick={() => downloadJSON(`${savedConfig.name}.json`, exportAgent(savedConfig))}>
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
