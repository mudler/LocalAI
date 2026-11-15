// eslint-disable-next-line no-unused-vars
import { Fragment, useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatSkillsApi } from '../utils/api'
import { skillRef } from '../utils/chatSkills'
import { saveBlob } from '../utils/download'
import { useLibraryFacts } from '../hooks/useLibraryFacts'
import { useWideLayout } from '../hooks/useWideLayout'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SearchField, Segmented } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
// eslint-disable-next-line no-unused-vars
import { AddToMenu, UsedByStrip } from '../components/library/LibraryBits'
// eslint-disable-next-line no-unused-vars
import { CountLine, DensityToggle, NoMatch, RailHeading, RailRow, RailSelect, RailSkeleton, ShowMore, SortMenu } from '../components/library/RailBits'
import { useRevealSelected, useRovingRows, useSlashToFocus } from '../hooks/useRail'
import { useStoredChoice } from '../hooks/useStoredChoice'
import { usedByLine } from '../utils/libraryText'
import { estimateTokens, skillUsers, withSkill, withoutSkill } from '../utils/library'
import { matchesQuery, rowLimit, runsOf, sortItems } from '../utils/libraryList'
import './library.css'

const PREVIEW_LINES = 8
// The orders the list offers, the densities of a row, and how many rows it draws at a time.
const SORTS = ['source', 'name', 'name-desc', 'used']
const DENSITIES = ['comfortable', 'compact']
const ROWS_PAGE = 60
// The layers, nearest first: a skill of a nearer layer hides one of the same name.
const LAYERS = ['personal', 'agent', 'folder', 'shared', 'addon', 'builtin']

// Two add-ons may ship a skill of one name, so an add-on skill keys by its add-on too.
// An agent skill keys by its agent: two agents may keep one name.
const keyOf = (s) => (s.layer === 'addon' ? `addon/${s.addon}/${s.name}` : s.layer === 'agent' ? `agent/${s.agent}/${s.name}` : `${s.layer}/${s.name}`)

// The badge of a skill: the add-on's title for an add-on skill, else the layer.
const badgeOf = (t, s) => (s.layer === 'addon' ? (s.addon_title || s.addon) : s.layer === 'agent' ? (s.agent_name || s.agent) : t(`layers.${s.layer}`))

// Why a skill does not serve, or where a serving one comes from, in one line.
// titles maps an add-on ID to its title, from the add-on skills of the list.
function statusOf(t, s, titles = {}) {
  if (s.invalid) return t('card.invalid')
  // An add-on skill of a name that another, chosen add-on serves is not
  // active; a built-in skill that an add-on serves is replaced.
  if (s.shadowed_by && s.layer === 'addon') return t('card.notActiveBy', { name: titles[s.shadowed_by] || s.shadowed_by })
  if (s.shadowed_by) return t('card.replacedBy', { name: titles[s.shadowed_by] || s.shadowed_by })
  if (s.shadowed === 'builtin' && s.layer === 'addon') return t('card.notActive')
  if (s.shadowed) return t('card.shadowed', { layer: t(`layers.${s.shadowed}`) })
  return ''
}

export default function Skills() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { t } = useTranslation(['skills', 'library'])
  const facts = useLibraryFacts()
  const wide = useWideLayout()

  const [skills, setSkills] = useState([])
  const [notes, setNotes] = useState([])
  const [search, setSearchNow] = useState('')
  const [usage, setUsageNow] = useState('all')
  const [layer, setLayerNow] = useState('all')
  const [sort, setSort] = useStoredChoice('localai.library.skills.sort', 'source', SORTS)
  const [density, setDensity] = useStoredChoice('localai.library.density', 'comfortable', DENSITIES)
  const [shown, setShown] = useState(ROWS_PAGE)
  const searchRef = useRef(null)
  const scrollRef = useRef(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [off, setOff] = useState('')
  const [toDelete, setToDelete] = useState(null)
  const [undo, setUndo] = useState(null)
  const [busy, setBusy] = useState(false)
  const [importing, setImporting] = useState(false)
  const [imported, setImported] = useState(null) // the answer of the last import
  const [importError, setImportError] = useState('')
  const zipInput = useRef(null)

  const select = useCallback((key) => setParams(key ? { skill: key } : {}), [setParams])

  const fetchSkills = useCallback(async () => {
    try {
      const data = await chatSkillsApi.list('', { agents: true })
      setSkills(data.skills || [])
      setNotes(data.notes || [])
      setOff('')
      setError(null)
    } catch (err) {
      // A failed read is said in place of the list; it never looks empty.
      if (err?.status === 409 && err.body?.error?.type === 'files_off') setOff(err.body.error.message)
      else setError(err)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchSkills() }, [fetchSkills])

  // Who uses a skill is read by its name: that is how an agent lists it.
  const usersOf = useMemo(() => {
    const map = new Map()
    for (const s of skills) map.set(s.name, skillUsers(facts.agents, s.name))
    return map
  }, [skills, facts.agents])
  const known = facts.status === 'ready'

  // A new search or filter starts the list from its top again.
  const setSearch = (v) => { setSearchNow(v); setShown(ROWS_PAGE) }
  const setUsage = (v) => { setUsageNow(v); setShown(ROWS_PAGE) }
  const setLayer = (v) => { setLayerNow(v); setShown(ROWS_PAGE) }
  const clearFilters = () => { setSearch(''); setUsage('all'); setLayer('all') }
  useSlashToFocus(searchRef)

  const layersPresent = LAYERS.filter(l => skills.some(s => s.layer === l))
  const addonTitles = useMemo(() => Object.fromEntries(skills.filter(s => s.layer === 'addon').map(s => [s.addon, s.addon_title || s.addon])), [skills])
  const visible = useMemo(() => {
    const rows = skills.filter(s => {
      if (layer !== 'all' && s.layer !== layer) return false
      if (!matchesQuery(search, s.name, s.description)) return false
      if (known && usage === 'used' && usersOf.get(s.name).length === 0) return false
      if (known && usage === 'unused' && usersOf.get(s.name).length > 0) return false
      return true
    })
    const mode = known || sort !== 'used' ? sort : 'name'
    return sortItems(rows, mode, s => ({ name: s.name, used: usersOf.get(s.name).length, group: s.layer }), LAYERS)
  }, [skills, search, layer, usage, sort, known, usersOf])
  const counts = useMemo(() => {
    const used = skills.filter(s => usersOf.get(s.name).length > 0).length
    return { all: skills.length, used, unused: skills.length - used }
  }, [skills, usersOf])
  const layerCounts = useMemo(() => {
    const m = new Map()
    for (const s of skills) m.set(s.layer, (m.get(s.layer) || 0) + 1)
    return m
  }, [skills])

  // Side by side, the first skill is open until another is picked. On a phone
  // nothing opens until the person taps one.
  const asked = params.get('skill') || ''
  const selected = skills.find(s => keyOf(s) === asked) || (wide && !asked ? visible[0] : null) || null
  const open = Boolean(selected)

  // The rows drawn: a page of them, widened when the open one lies further down.
  const drawn = visible.slice(0, rowLimit(shown, visible.length, selected ? visible.findIndex(s => keyOf(s) === keyOf(selected)) : -1))
  const roving = useRovingRows(drawn.map(keyOf), selected ? keyOf(selected) : '')
  useRevealSelected(scrollRef, selected ? keyOf(selected) : '')
  const filtered = search.trim() !== '' || usage !== 'all' || layer !== 'all'
  const grouped = sort === 'source'

  const deleteSkill = async (skill) => {
    setToDelete(null)
    try {
      await chatSkillsApi.remove(skill.layer, skill.name, skillRef(skill))
      addToast(t('toasts.deleted', { name: skill.name }), 'success')
      select('')
      fetchSkills()
    } catch (err) {
      addToast(err.message || t('toasts.deleteFailed'), 'error')
    }
  }

  // ---- Import ------------------------------------------------------------
  // A zip file in the Agent Skills format. The server checks all of it and
  // stores nothing when any part is not valid; scripts are stored, not run.
  const importZip = async (e) => {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    setImporting(true)
    setImported(null)
    setImportError('')
    try {
      const res = await chatSkillsApi.importZip(file)
      setImported(res)
      fetchSkills()
    } catch (err) {
      setImportError(err.message || t('import.failed'))
    } finally {
      setImporting(false)
    }
  }
  const exportSkill = async (skill) => {
    try {
      saveBlob(`${skill.name}.zip`, await chatSkillsApi.exportZip(skill.name))
    } catch (err) {
      addToast(t('toasts.exportFailed', { message: err.message }), 'error')
    }
  }
  // The quiet menu of a row: the same actions the open skill offers in its pane.
  const menuFor = (skill) => [
    ...(skill.layer === 'personal' && !skill.invalid ? [{ key: 'export', icon: 'download', label: t('actions.export'), onClick: () => exportSkill(skill) }] : []),
    ...(skill.editable ? [{ key: 'edit', icon: 'edit', label: t('actions.edit'), onClick: () => navigate(`/app/skills/edit/${encodeURIComponent(skill.layer)}/${encodeURIComponent(skill.name)}`) }] : []),
    ...(skill.editable || skill.removable ? [{ divider: true }, { key: 'delete', icon: 'trash', danger: true, label: t('actions.delete'), onClick: () => setToDelete(skill) }] : []),
  ]
  const importButton = (
    <>
      <Button variant="secondary" icon="upload" busy={importing} onClick={() => zipInput.current?.click()} data-testid="skills-import">{t('actions.import')}</Button>
      <input ref={zipInput} type="file" accept=".zip,application/zip" hidden onChange={importZip} data-testid="skills-import-input" />
    </>
  )

  // ---- Attach and detach -------------------------------------------------
  const configOf = (agent) => facts.agents.find(a => a.name === agent)?.config || null

  const saveFor = async (agent, next) => {
    setBusy(true)
    try {
      await facts.saveConfig(agent, next)
      return true
    } catch (err) {
      addToast(t('library:toasts.updateFailed', { agent, message: err.message }), 'error')
      return false
    } finally {
      setBusy(false)
    }
  }

  const addToAgent = async (skill, agent) => {
    const config = configOf(agent)
    if (!config) { addToast(t('library:toasts.updateFailed', { agent, message: t('library:usedBy.unread') }), 'error'); return }
    const next = withSkill(config, skill.name)
    if (!next) return
    if (await saveFor(agent, next)) addToast(t('library:toasts.skillAdded', { name: skill.name, agent }), 'success')
  }

  const removeFromAgent = async (skill, agent) => {
    const config = configOf(agent)
    const next = config && withoutSkill(config, skill.name)
    if (!next) return
    if (!(await saveFor(agent, next))) return
    setUndo({
      id: Date.now(),
      message: t('library:toasts.skillRemoved', { name: skill.name, agent }),
      restore: async () => {
        if (await saveFor(agent, config)) addToast(t('library:toasts.undone'), 'success')
      },
    })
  }

  if (off) {
    return (
      <Page width="full" className="lib-page" data-testid="skills-page">
        <PageHeader title={t('title')} supporting={t('unavailable.subtitle')} />
        <div role="note"><EmptyState variant="unavailable" testId="files-off" title={off} /></div>
      </Page>
    )
  }

  const empty = !loading && !error && skills.length === 0

  return (
    <Page width="full" className="lib-page" data-testid="skills-page">
      <PageHeader
        title={t('title')}
        supporting={t('subtitle')}
        actions={!error && (
          <>
            {importButton}
            {!empty && <Button variant="primary" icon="plus" onClick={() => navigate('/app/skills/new')}>{t('actions.newSkill')}</Button>}
          </>
        )}
      />

      {importError && (
        <Callout tone="error" role="alert" title={t('import.failedTitle')} testId="skills-import-error"
          actions={<Button size="sm" variant="ghost" onClick={() => setImportError('')}>{t('import.dismiss')}</Button>}>
          {importError}
        </Callout>
      )}
      {imported && (
        <Callout tone="ok" title={t('import.done', { count: imported.skills.length })} testId="skills-import-done"
          actions={<Button size="sm" variant="ghost" onClick={() => setImported(null)}>{t('import.dismiss')}</Button>}>
          ({imported.skills.map(k => k.name).join(', ')}).
          {` ${t('import.scriptsStored')} ${t('import.newNote')}`}
        </Callout>
      )}

      {notes.map(n => <p key={n} className="lib-note lib-note--warn" role="note">{n}</p>)}

      {loading ? (
        <RailSkeleton />
      ) : error ? (
        <LoadError title={t('load.title')} error={error} onRetry={fetchSkills} />
      ) : empty ? (
        <EmptyState
          testId="skills-empty"
          title={t('empty.title')}
          body={t('empty.text')}
          steps={[t('empty.stepWrite'), t('empty.stepTry'), t('empty.stepAdd')]}
          actions={<Button variant="primary" icon="plus" onClick={() => navigate('/app/skills/new')}>{t('actions.createSkill')}</Button>}
        />
      ) : (
        <div className="dk-split lib-split" data-view={open ? 'pane' : 'list'}>
          <div className="dk-split-rail lib-rail" data-density={density}>
            <div className="lib-bar">
              <div className="lib-bar__row">
                <SearchField
                  ref={searchRef}
                  value={search}
                  onChange={setSearch}
                  label={t('search.label')}
                  placeholder={t('search.placeholder')}
                  testId="skills-search"
                  onKeyDown={(e) => { if (e.key === 'Escape' && search) { e.stopPropagation(); setSearch('') } }}
                />
                <DensityToggle value={density} onChange={setDensity} testId="skills-density" />
              </div>
              <div className="lib-bar__row">
                <Segmented
                  options={['all', 'used', 'unused'].map(f => ({ id: f, label: t(`filters.${f}`), count: known ? counts[f] : undefined, disabled: f !== 'all' && !known }))}
                  value={usage}
                  onChange={setUsage}
                  label={t('filters.label')}
                  itemTestId="skills-filter-"
                />
                <SortMenu
                  label={t('sort.label')}
                  value={sort}
                  onChange={setSort}
                  options={SORTS.filter(id => known || id !== 'used').map(id => ({ id, label: t(`sort.${id}`) }))}
                  testId="skills-sort"
                />
              </div>
              {layersPresent.length > 1 && (
                <div className="lib-bar__row">
                  <RailSelect
                    label={t('source.label')}
                    value={layer}
                    onChange={setLayer}
                    options={[{ id: 'all', label: t('source.all', { count: skills.length }) }, ...layersPresent.map(l => ({ id: l, label: `${t(`layers.${l}`)} (${layerCounts.get(l)})` }))]}
                    testId="skills-layer"
                  />
                </div>
              )}
            </div>
            <CountLine
              text={filtered ? t('list.countOf', { shown: visible.length, total: skills.length }) : t('list.countAll', { count: skills.length })}
              filtered={filtered}
              onClear={clearFilters}
              testId="skills-count"
            />

            <div className="lib-scroll" ref={scrollRef} onKeyDown={roving.onKeyDown}>
              {visible.length === 0 ? (
                <NoMatch text={t('list.none')} onClear={clearFilters} testId="skills-none" />
              ) : (
                <ul className="lib-rows" role="list" aria-label={t('list.label')}>
                  {runsOf(drawn, s => (grouped ? s.layer : '')).map(run => (
                    <Fragment key={run.group || 'all'}>
                      {grouped && layersPresent.length > 1 && <RailHeading count={visible.filter(s => s.layer === run.group).length}>{t(`layers.${run.group}`)}</RailHeading>}
                      {run.items.map(s => (
                        <RailRow
                          key={keyOf(s)}
                          rowKey={keyOf(s)}
                          testId={`skill-row-${s.name}`}
                          selected={selected && keyOf(selected) === keyOf(s)}
                          stop={roving.stopKey === keyOf(s)}
                          onOpen={() => select(keyOf(s))}
                          onFocus={() => roving.onRowFocus(keyOf(s))}
                          icon="sparkles"
                          mono
                          title={s.name}
                          meta={statusOf(t, s, addonTitles) || (s.fallback ? t('card.fallback', { name: s.fallback }) : usedByLine(t, usersOf.get(s.name), facts.status))}
                          metaTestId={`skill-used-${s.name}`}
                          end={(
                            <>
                              {s.new && <span className="dk-badge" data-testid={`skill-new-${s.name}`} title={t('card.newTitle')}>{t('card.new')}</span>}
                              <span className="dk-badge" data-testid={`skill-badge-${s.name}`}>{badgeOf(t, s)}</span>
                            </>
                          )}
                          menu={menuFor(s)}
                          menuLabel={t('list.menu', { name: s.name })}
                        />
                      ))}
                    </Fragment>
                  ))}
                  <ShowMore remaining={visible.length - drawn.length} step={ROWS_PAGE} onMore={() => setShown(n => n + ROWS_PAGE)} testId="skills-more" />
                </ul>
              )}
            </div>
          </div>

          <section className="dk-card lib-pane" aria-live="polite" data-testid="skill-pane">
            {selected ? (
              <SkillPane
                key={keyOf(selected)}
                t={t}
                skill={selected}
                titles={addonTitles}
                users={usersOf.get(selected.name) || []}
                status={facts.status}
                agents={facts.agents}
                busy={busy}
                onBack={() => select('')}
                onEdit={(s) => navigate(`/app/skills/edit/${encodeURIComponent(s.layer)}/${encodeURIComponent(s.name)}`)}
                onDelete={setToDelete}
                onExport={exportSkill}
                onAdd={(agent) => addToAgent(selected, agent)}
                onRemove={(agent) => removeFromAgent(selected, agent)}
              />
            ) : (
              <div className="lib-pane__empty" data-testid="skill-pane-empty">
                <p>{t('pane.pick')}</p>
              </div>
            )}
          </section>
        </div>
      )}

      {undo && (
        <HomeUndoToast
          key={undo.id}
          message={undo.message}
          testId="library-undo-toast"
          undoLabel={t('library:toasts.undo')}
          dismissLabel={t('library:toasts.dismiss')}
          onUndo={() => { const { restore } = undo; setUndo(null); restore() }}
          onExpire={() => setUndo(null)}
        />
      )}

      <ChatDeleteConfirm item={toDelete} kind="skill" onConfirm={deleteSkill} onCancel={() => setToDelete(null)} />
    </Page>
  )
}

// eslint-disable-next-line no-unused-vars
function SkillPane({ t, skill, titles, users, status, agents, busy, onBack, onEdit, onDelete, onExport, onAdd, onRemove }) {
  const [tab, setTab] = useState('overview')
  const [detail, setDetail] = useState({ phase: 'loading', data: null })
  const unused = status === 'ready' && users.length === 0
  // An agent skill belongs to its agent: no other agent is given it from here.
  const grantable = !skill.invalid && skill.layer !== 'agent'

  useEffect(() => {
    if (skill.invalid) { setDetail({ phase: 'failed', data: null }); return undefined }
    let live = true
    chatSkillsApi.get(skill.layer, skill.name, skillRef(skill))
      .then(data => { if (live) setDetail({ phase: 'ready', data }) })
      .catch(() => { if (live) setDetail({ phase: 'failed', data: null }) })
    return () => { live = false }
  }, [skill.layer, skill.name, skill.addon, skill.agent, skill.invalid])

  const body = detail.data?.body || ''
  const tokens = estimateTokens(body)
  const tabs = ['overview', 'instructions']

  return (
    <>
      <Button variant="ghost" size="sm" icon="arrow-left" className="lib-back" onClick={onBack}>{t('pane.back')}</Button>

      <div className="lib-pane__head">
        <div className="lib-pane__title">
          <h2 className="lib-mono" data-testid="skill-title">{skill.name}</h2>
          <p className="lib-pane__sub">{skill.description || t('card.noDescription')}</p>
        </div>
        <div className="lib-pane__acts">
          {skill.layer === 'personal' && !skill.invalid && (
            <Button size="sm" icon="download" onClick={() => onExport(skill)} title={t('card.exportTitle')} data-testid="skill-export">{t('actions.export')}</Button>
          )}
          {skill.editable && (
            <Button size="sm" icon="edit" onClick={() => onEdit(skill)} title={t('card.editTitle')}>{t('actions.edit')}</Button>
          )}
          {(skill.editable || skill.removable) && (
            <Button variant="danger-ghost" size="sm" icon="trash" onClick={() => onDelete(skill)} title={t('card.deleteTitle')}>{t('actions.delete')}</Button>
          )}
        </div>
      </div>

      <ul className="lib-facts">
        <li className="lib-fact" data-testid="skill-layer">{skill.layer === 'addon' ? t('card.providedBy', { name: badgeOf(t, skill) }) : skill.layer === 'agent' ? t('card.proposedBy', { name: badgeOf(t, skill) }) : t(`layers.${skill.layer}`)}</li>
        {!skill.editable && <li className="lib-fact">{t('card.readOnly')}</li>}
        {skill.shadowed && <li className="lib-fact" data-testid="skill-shadowed">{skill.shadowed_by || skill.layer === 'addon' ? statusOf(t, skill, titles) : t('card.shadowedLong', { layer: t(`layers.${skill.shadowed}`) })}</li>}
        {skill.fallback && <li className="lib-fact" data-testid="skill-fallback">{t('card.fallback', { name: skill.fallback })}</li>}
        {detail.phase === 'ready' && <li className="lib-fact" data-testid="skill-tokens" title={t('pane.tokensTitle')}>{t('pane.tokens', { count: tokens })}</li>}
      </ul>

      {skill.new && <p className="lib-note lib-note--warn" role="note" data-testid="skill-new">{t('card.newLong')}</p>}

      {skill.invalid && <p className="lib-note lib-note--error" role="alert" data-testid="skill-invalid">{t('card.invalidWhy', { reason: skill.invalid })}</p>}

      {grantable && (
        <>
          <UsedByStrip users={users} status={status} onRemove={onRemove} busy={busy}>
            <AddToMenu kind="skill" name={skill.name} agents={agents.filter(a => a.config)} status={status} onAdd={onAdd} busy={busy} />
          </UsedByStrip>
          {unused && (
            <div className="lib-notused" data-testid="skill-not-used">
              <p>{t('library:notUsed.skillShort')}</p>
            </div>
          )}
        </>
      )}

      <div className="dk-tabs lib-tabs" role="tablist" aria-label={t('pane.tabs')}>
        {tabs.map(id => (
          <button
            key={id}
            type="button"
            role="tab"
            id={`skill-tab-${id}`}
            aria-controls={`skill-panel-${id}`}
            aria-selected={tab === id}
            tabIndex={tab === id ? 0 : -1}
            className="dk-tab"
            onClick={() => setTab(id)}
            onKeyDown={(e) => {
              const i = tabs.indexOf(id)
              const next = e.key === 'ArrowRight' ? tabs[(i + 1) % tabs.length] : e.key === 'ArrowLeft' ? tabs[(i + tabs.length - 1) % tabs.length] : null
              if (next) { e.preventDefault(); setTab(next); document.getElementById(`skill-tab-${next}`)?.focus() }
            }}
          >
            {t(`pane.tab.${id}`)}
          </button>
        ))}
      </div>

      <div className="dk-tabpanel lib-section" role="tabpanel" id={`skill-panel-${tab}`} aria-labelledby={`skill-tab-${tab}`} tabIndex={0}>
        {detail.phase === 'loading' ? (
          <p className="lib-muted" aria-busy="true">{t('pane.loading')}</p>
        ) : detail.phase === 'failed' ? (
          !skill.invalid && <p className="lib-note lib-note--error" role="alert">{t('pane.loadFailed')}</p>
        ) : tab === 'overview' ? (
          body ? (
            <div className="lib-section">
              <h3 className="lib-eyebrow">{t('pane.startsWith')}</h3>
              <pre className="lib-pre lib-pre--preview" data-testid="skill-preview">{body.split('\n').slice(0, PREVIEW_LINES).join('\n')}</pre>
              <button type="button" className="dk-link lib-more" onClick={() => setTab('instructions')}>{t('pane.showAll')}</button>
            </div>
          ) : <p className="lib-muted">{t('pane.noContent')}</p>
        ) : (
          body
            ? <pre className="lib-pre" data-testid="skill-content">{body}</pre>
            : <p className="lib-muted">{t('pane.noContent')}</p>
        )}
      </div>
    </>
  )
}
