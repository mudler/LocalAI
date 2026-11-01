import { useState, useEffect, useCallback, useMemo } from 'react'
import { useNavigate, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatSkillsApi } from '../utils/api'
import { useLibraryFacts } from '../hooks/useLibraryFacts'
import { useWideLayout } from '../hooks/useWideLayout'
// eslint-disable-next-line no-unused-vars
import { Button, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../components/home/HomeUndoToast'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { AddToMenu, UsedByStrip } from '../components/library/LibraryBits'
import { usedByLine } from '../utils/libraryText'
import { estimateTokens, skillUsers, withSkill, withoutSkill } from '../utils/library'
import './library.css'

const PREVIEW_LINES = 8
// The layers, nearest first: a skill of a nearer layer hides one of the same name.
const LAYERS = ['personal', 'folder', 'shared', 'addon', 'builtin']

// Two add-ons may ship a skill of one name, so an add-on skill keys by its add-on too.
const keyOf = (s) => (s.layer === 'addon' ? `addon/${s.addon}/${s.name}` : `${s.layer}/${s.name}`)

// The badge of a skill: the add-on's title for an add-on skill, else the layer.
const badgeOf = (t, s) => (s.layer === 'addon' ? (s.addon_title || s.addon) : t(`layers.${s.layer}`))

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
  const [search, setSearch] = useState('')
  const [usage, setUsage] = useState('all')
  const [layer, setLayer] = useState('all')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [off, setOff] = useState('')
  const [toDelete, setToDelete] = useState(null)
  const [undo, setUndo] = useState(null)
  const [busy, setBusy] = useState(false)

  const select = useCallback((key) => setParams(key ? { skill: key } : {}), [setParams])

  const fetchSkills = useCallback(async () => {
    try {
      const data = await chatSkillsApi.list()
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

  const layersPresent = LAYERS.filter(l => skills.some(s => s.layer === l))
  const addonTitles = useMemo(() => Object.fromEntries(skills.filter(s => s.layer === 'addon').map(s => [s.addon, s.addon_title || s.addon])), [skills])
  const visible = useMemo(() => {
    const q = search.trim().toLowerCase()
    return skills.filter(s => {
      if (layer !== 'all' && s.layer !== layer) return false
      if (q && !s.name.toLowerCase().includes(q) && !(s.description || '').toLowerCase().includes(q)) return false
      if (known && usage === 'used' && usersOf.get(s.name).length === 0) return false
      if (known && usage === 'unused' && usersOf.get(s.name).length > 0) return false
      return true
    })
  }, [skills, search, layer, usage, known, usersOf])

  // Side by side, the first skill is open until another is picked. On a phone
  // nothing opens until the person taps one.
  const asked = params.get('skill') || ''
  const selected = skills.find(s => keyOf(s) === asked) || (wide && !asked ? visible[0] : null) || null
  const open = Boolean(selected)

  const deleteSkill = async (skill) => {
    setToDelete(null)
    try {
      await chatSkillsApi.remove(skill.layer, skill.name)
      addToast(t('toasts.deleted', { name: skill.name }), 'success')
      select('')
      fetchSkills()
    } catch (err) {
      addToast(err.message || t('toasts.deleteFailed'), 'error')
    }
  }

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
        actions={!empty && !error && (
          <Button variant="primary" icon="plus" onClick={() => navigate('/app/skills/new')}>{t('actions.newSkill')}</Button>
        )}
      />

      {notes.map(n => <p key={n} className="lib-note lib-note--warn" role="note">{n}</p>)}

      {loading ? (
        <SkeletonRows count={4} />
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
          <div className="dk-split-rail lib-rail">
            <span className="dk-input-icon lib-find">
              <Icon name="search" className="dk-icon" />
              <input
                type="search"
                className="dk-input"
                placeholder={t('search.placeholder')}
                aria-label={t('search.label')}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                data-testid="skills-search"
              />
            </span>
            <div className="lib-filters" role="group" aria-label={t('filters.label')}>
              {['all', 'used', 'unused'].map(f => (
                <button
                  key={f}
                  type="button"
                  className="dk-chip dk-chip--sm"
                  aria-pressed={usage === f}
                  disabled={f !== 'all' && !known}
                  onClick={() => setUsage(f)}
                  data-testid={`skills-filter-${f}`}
                >
                  {t(`filters.${f}`)}
                </button>
              ))}
            </div>
            {layersPresent.length > 1 && (
              <div className="lib-filters" role="group" aria-label={t('layers.label')}>
                {['all', ...layersPresent].map(l => (
                  <button
                    key={l}
                    type="button"
                    className="dk-chip dk-chip--sm"
                    aria-pressed={layer === l}
                    onClick={() => setLayer(l)}
                    data-testid={`skills-layer-${l}`}
                  >
                    {l === 'all' ? t('filters.all') : t(`layers.${l}`)}
                  </button>
                ))}
              </div>
            )}

            {visible.length === 0 ? (
              <p className="lib-rail__none" data-testid="skills-none">{t('list.none')}</p>
            ) : (
              <div className="dk-list lib-rows" role="list" aria-label={t('list.label')}>
                {visible.map(s => (
                  <button
                    key={keyOf(s)}
                    type="button"
                    role="listitem"
                    className="dk-row"
                    aria-selected={selected && keyOf(selected) === keyOf(s)}
                    onClick={() => select(keyOf(s))}
                    data-testid={`skill-row-${s.name}`}
                  >
                    <span className="dk-row-lead"><Icon name="sparkles" /></span>
                    <span className="dk-row-main">
                      <span className="dk-row-title dk-mono">{s.name}</span>
                      <span className="dk-row-meta" data-testid={`skill-used-${s.name}`}>
                        {statusOf(t, s, addonTitles) || (s.fallback ? t('card.fallback', { name: s.fallback }) : usedByLine(t, usersOf.get(s.name), facts.status))}
                      </span>
                    </span>
                    <span className="dk-row-end"><span className="dk-badge" data-testid={`skill-badge-${s.name}`}>{badgeOf(t, s)}</span></span>
                  </button>
                ))}
              </div>
            )}
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
function SkillPane({ t, skill, titles, users, status, agents, busy, onBack, onEdit, onDelete, onAdd, onRemove }) {
  const [tab, setTab] = useState('overview')
  const [detail, setDetail] = useState({ phase: 'loading', data: null })
  const unused = status === 'ready' && users.length === 0
  const grantable = !skill.invalid

  useEffect(() => {
    if (skill.invalid) { setDetail({ phase: 'failed', data: null }); return undefined }
    let live = true
    chatSkillsApi.get(skill.layer, skill.name, skill.layer === 'addon' ? skill.addon : '')
      .then(data => { if (live) setDetail({ phase: 'ready', data }) })
      .catch(() => { if (live) setDetail({ phase: 'failed', data: null }) })
    return () => { live = false }
  }, [skill.layer, skill.name, skill.addon, skill.invalid])

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
          {skill.editable && (
            <Button size="sm" icon="edit" onClick={() => onEdit(skill)} title={t('card.editTitle')}>{t('actions.edit')}</Button>
          )}
          {skill.editable && (
            <Button variant="danger-ghost" size="sm" icon="trash" onClick={() => onDelete(skill)} title={t('card.deleteTitle')}>{t('actions.delete')}</Button>
          )}
        </div>
      </div>

      <ul className="lib-facts">
        <li className="lib-fact" data-testid="skill-layer">{skill.layer === 'addon' ? t('card.providedBy', { name: badgeOf(t, skill) }) : t(`layers.${skill.layer}`)}</li>
        {!skill.editable && <li className="lib-fact">{t('card.readOnly')}</li>}
        {skill.shadowed && <li className="lib-fact" data-testid="skill-shadowed">{skill.shadowed_by || skill.layer === 'addon' ? statusOf(t, skill, titles) : t('card.shadowedLong', { layer: t(`layers.${skill.shadowed}`) })}</li>}
        {skill.fallback && <li className="lib-fact" data-testid="skill-fallback">{t('card.fallback', { name: skill.fallback })}</li>}
        {detail.phase === 'ready' && <li className="lib-fact" data-testid="skill-tokens" title={t('pane.tokensTitle')}>{t('pane.tokens', { count: tokens })}</li>}
      </ul>

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
