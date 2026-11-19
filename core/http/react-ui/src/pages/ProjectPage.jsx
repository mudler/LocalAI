/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatChangesApi, chatProjectsApi, chatsApi, folderSharesApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import ShareDialog from '../components/share/ShareDialog'
import { folderShareClient } from '../components/share/shareClients'
import { CAP_CHAT } from '../utils/capabilities'
import { useModels } from '../hooks/useModels'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import Icon from '../components/Icon'
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import ProjectSheet from '../components/projects/ProjectSheet'
import ProjectChats, { ChatRows, chatPath } from '../components/projects/ProjectChats'
import ProjectAgents from '../components/projects/ProjectAgents'
import ProjectConnections from '../components/projects/ProjectConnections'
import ProjectAutomations from '../components/projects/ProjectAutomations'
import ProjectFiles from '../components/projects/ProjectFiles'
import ProjectMemory from '../components/projects/ProjectMemory'
import ProjectSkills from '../components/projects/ProjectSkills'
import ProjectTools from '../components/projects/ProjectTools'
import { deleteProjectWithUndo } from '../components/projects/projectUndo'
import { ProjectStatus } from './Projects'
import { readChatStart, readLastModel } from '../utils/lastModel'
import { proposalsForProject } from '../utils/changeProposal'
import { defaultsCount, memoryTopicCount, isProjectsOff, pickStartModel, startChatBody, whenAgo, wrapKey } from '../utils/projects'
import './projects.css'

const TABS = ['overview', 'files', 'chats', 'agents', 'connections', 'automations', 'memory', 'skills', 'tools']

export default function ProjectPage() {
  const { id } = useParams()
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t, i18n } = useTranslation(['projects', 'common', 'workspace'])
  const { authEnabled } = useAuth()
  const [params, setParams] = useSearchParams()
  const tab = TABS.includes(params.get('tab')) ? params.get('tab') : 'overview'
  const [project, setProject] = useState(null)
  const [chats, setChats] = useState([])
  const [memory, setMemory] = useState({ index: '', files: [] })
  const [state, setState] = useState('loading') // loading | ready | error | off | missing
  const [error, setError] = useState(null)
  const [off, setOff] = useState('')
  const [editing, setEditing] = useState(false)
  const [starting, setStarting] = useState(false)
  // The Delete confirm: null while closed, else what the delete touches.
  const [confirmDelete, setConfirmDelete] = useState(null)
  const [tabsEdge, setTabsEdge] = useState({ start: false, end: false })
  const tabsRef = useRef(null)
  const { models, loading: modelsLoading } = useModels(CAP_CHAT)
  const tabRefs = useRef({})
  // Sharing the project folder. It is offered only where folders can be
  // shared (accounts on and the server allows it); without that the button is
  // not drawn. `shares` is { available, count } from the folder shares list.
  // How many proposed notes for this project's memory wait for the owner.
  const [waiting, setWaiting] = useState(0)
  const [shares, setShares] = useState({ available: false, count: 0 })
  const [sharing, setSharing] = useState(null)
  const folder = project ? `/Projects/${project.slug}` : ''
  const loadShares = useCallback(async () => {
    if (!authEnabled || !folder) return
    try {
      const d = await folderSharesApi.list()
      const mine = (d?.folders || []).find(f => f.path === folder)
      setShares({ available: d?.sharing === true, count: mine?.sharedWith || 0 })
    } catch { /* the Share button stays hidden; the project works without it */ }
  }, [authEnabled, folder])
  useEffect(() => { loadShares() }, [loadShares])
  const slug = project?.slug
  useEffect(() => {
    if (!slug) return undefined
    let live = true
    chatChangesApi.list('pending')
      .then((l) => { if (live) setWaiting(proposalsForProject(l, slug).length) })
      .catch(() => { if (live) setWaiting(0) })
    return () => { live = false }
  }, [slug])
  const shareClient = useMemo(() => (sharing ? folderShareClient(folderSharesApi, sharing.id) : null), [sharing])
  const shareRoles = useMemo(() => [
    { id: 'read', label: t('shared.mode.read', { ns: 'workspace' }), help: t('share.readHelp', { ns: 'workspace' }) },
    { id: 'write', label: t('shared.mode.write', { ns: 'workspace' }), help: t('share.writeHelp', { ns: 'workspace' }) },
  ], [t])
  const openShare = async () => {
    try {
      const f = await folderSharesApi.find(folder)
      setSharing({ id: f.id, name: project.name })
    } catch (e) {
      addToast(t('share.failed', { ns: 'workspace', message: e?.message || String(e) }), 'error')
    }
  }

  // The project, its chats and its memory. The chats and the memory are best
  // effort: a project that loads with a failed side read still opens.
  const load = useCallback(async ({ quiet = false } = {}) => {
    if (!quiet) setState('loading')
    try {
      const p = await chatProjectsApi.get(id)
      const [c, m] = await Promise.all([
        chatProjectsApi.chats(id).catch(() => ({ conversations: [] })),
        chatProjectsApi.memory(id).catch(() => ({ index: '', files: [] })),
      ])
      setProject(p)
      setChats(c.conversations || [])
      setMemory({ index: m.index || '', files: m.files || [] })
      setState('ready')
    } catch (err) {
      if (isProjectsOff(err)) { setOff(err.body.error.message); setState('off') }
      else if (err?.status === 404) setState('missing')
      else if (quiet) addToast(t('toasts.actionFailed', { message: err.message }), 'error')
      else { setError(err); setState('error') }
    }
  }, [id, addToast, t])

  useEffect(() => { load() }, [load])

  // On a narrow screen the strip scrolls: keep the open tab in view.
  useEffect(() => {
    tabRefs.current[tab]?.scrollIntoView?.({ inline: 'center', block: 'nearest' })
  }, [tab, state])

  // Which edges of the tab strip have tabs hidden behind them, for the fades.
  const measureTabs = useCallback(() => {
    const el = tabsRef.current
    if (!el) return
    const start = el.scrollLeft > 1
    const end = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
    setTabsEdge(prev => (prev.start === start && prev.end === end ? prev : { start, end }))
  }, [])
  useEffect(() => {
    const el = tabsRef.current
    if (!el) return undefined
    measureTabs()
    if (typeof ResizeObserver === 'undefined') return undefined
    const ro = new ResizeObserver(measureTabs)
    ro.observe(el)
    return () => ro.disconnect()
  }, [measureTabs, state, i18n.language])
  const scrollTabs = (dir) => tabsRef.current?.scrollBy?.({ left: dir * Math.max(120, tabsRef.current.clientWidth * 0.6), behavior: 'smooth' })

  const setTab = (next) => setParams(next === 'overview' ? {} : { tab: next }, { replace: true })
  const onTabKey = (e) => {
    const i = TABS.indexOf(tab)
    const to = wrapKey(e.key, i, TABS.length)
    if (to < 0) return
    e.preventDefault()
    const next = TABS[to]
    setTab(next)
    tabRefs.current[next]?.focus()
  }

  const startChat = async () => {
    // The model the Home composer would start with: the chosen default, else
    // the last model used, else the first one.
    const model = pickStartModel(models, readChatStart(), readLastModel(CAP_CHAT))
    if (!model) { addToast(t('toasts.noModel'), 'warning'); return }
    setStarting(true)
    try {
      const chat = await chatsApi.create(model, undefined, undefined, false, undefined, startChatBody(project).project_id)
      navigate(chatPath(chat))
    } catch (err) {
      addToast(t('toasts.chatFailed', { message: err.message }), 'error')
      setStarting(false)
    }
  }

  const act = async (verb) => {
    try {
      const p = await chatProjectsApi[verb](project.id)
      setProject(p && p.id ? p : { ...project, status: verb === 'archive' ? 'archived' : 'active' })
      addToast(t(verb === 'archive' ? 'toasts.archived' : 'toasts.restored', { name: project.name }), 'success')
      load({ quiet: true })
    } catch (err) {
      addToast(t('toasts.actionFailed', { message: err.message }), 'error')
    }
  }

  // Delete asks first. The dialog says what depends on the project: its agents
  // (which stop working), their triggers and its chats (which keep their
  // record but lose the project). Counts that cannot be read are left out.
  const askDelete = async () => {
    setConfirmDelete({ agents: project.agent_count || 0, chats: project.chat_count || chats.length, triggers: null })
    try {
      const r = await chatProjectsApi.triggers(project.id)
      setConfirmDelete(c => (c ? { ...c, triggers: (r.triggers || []).length } : c))
    } catch { /* the dialog still names the agents and chats */ }
  }
  const remove = () => {
    setConfirmDelete(null)
    deleteProjectWithUndo(project, { addToast, t, broken: project.status === 'broken' })
    navigate('/app/projects')
  }

  if (state === 'off') return <Page width="full"><PageHeader title={t('title')} /><div role="note"><EmptyState variant="unavailable" title={off} /></div></Page>
  if (state === 'missing') {
    return (
      <Page width="full" className="pj-page">
        <Link className="pj-back" to="/app/projects"><Icon name="arrow-left" />{t('back')}</Link>
        <EmptyState variant="no-results" title={t('load.notFound')} testId="project-missing" />
      </Page>
    )
  }
  if (state === 'error') {
    return (
      <Page width="full" className="pj-page">
        <Link className="pj-back" to="/app/projects"><Icon name="arrow-left" />{t('back')}</Link>
        <LoadError title={t('load.project')} error={error} onRetry={() => load()} />
      </Page>
    )
  }
  if (state === 'loading' || !project) {
    return <Page width="full" className="pj-page"><SkeletonRows count={3} /></Page>
  }

  const broken = project.status === 'broken'
  const archived = project.status === 'archived'
  const menu = [
    archived
      ? { key: 'restore', icon: 'undo', label: t('actions.restore'), onClick: () => act('restore') }
      : { key: 'archive', icon: 'inbox', label: t('actions.archive'), hidden: broken, onClick: () => act('archive') },
    { divider: true },
    { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: askDelete },
  ]

  return (
    <Page width="full" className="pj-page">
      <div className="pj-home">
        <Link className="pj-back" to="/app/projects"><Icon name="arrow-left" />{t('back')}</Link>
        <PageHeader
          title={project.name}
          supporting={project.description}
          actions={(
            <div className="pj-acts">
              {shares.available && !broken && <Button icon="users" onClick={openShare} data-testid="project-share">{t('actions.share')}</Button>}
              <Button icon="edit" disabled={broken || archived} title={archived ? t('actions.editArchived') : undefined} onClick={() => setEditing(true)} data-testid="project-edit">{t('actions.edit')}</Button>
              <ActionMenu ariaLabel={t('actions.more', { name: project.name })} items={menu} />
            </div>
          )}
        />
        <div className="pj-meta">
          <ProjectStatus status={project.status} />
          {project.updated && <span className="pj-muted"><Icon name="clock" />{t('updated', { when: whenAgo(project.updated, i18n.language) })}</span>}
          {shares.available && shares.count > 0 && <span className="pj-muted" data-testid="project-shared-count"><Icon name="users" />{t('share.sharedWith', { count: shares.count })}</span>}
        </div>

        {broken && <Callout tone="error" role="alert" title={t('broken.title')} testId="project-broken">{project.error || t('broken.fallback')}</Callout>}
        {archived && tab !== 'automations' && (
          <Callout tone="info" testId="project-archived" actions={<Button size="sm" onClick={() => act('restore')} data-testid="project-archived-restore">{t('actions.restore')}</Button>}>
            {t('archivedNote')}
          </Callout>
        )}

        {!broken && (
          <>
            <div className="pj-tabswrap" data-start={tabsEdge.start || undefined} data-end={tabsEdge.end || undefined} data-testid="project-tabs-wrap">
            <button type="button" className="pj-tabsmore pj-tabsmore--start" tabIndex={-1} aria-hidden="true" onClick={() => scrollTabs(-1)} data-testid="project-tabs-prev"><Icon name="chevron-left" /></button>
            <div className="pj-tabs" ref={tabsRef} onScroll={measureTabs} role="tablist" aria-label={t('tabs.label')} onKeyDown={onTabKey}>
              {TABS.map(k => (
                <button
                  key={k}
                  ref={el => { tabRefs.current[k] = el }}
                  type="button"
                  role="tab"
                  id={`project-tab-${k}`}
                  aria-selected={tab === k}
                  aria-controls={`project-panel-${k}`}
                  tabIndex={tab === k ? 0 : -1}
                  className="pj-tab"
                  data-testid={`project-tab-${k}`}
                  onClick={() => setTab(k)}
                >
                  {t(`tabs.${k}`)}
                </button>
              ))}
            </div>
            <button type="button" className="pj-tabsmore pj-tabsmore--end" tabIndex={-1} aria-hidden="true" onClick={() => scrollTabs(1)} data-testid="project-tabs-next"><Icon name="chevron-right" /></button>
            </div>
            <div role="tabpanel" id={`project-panel-${tab}`} aria-labelledby={`project-tab-${tab}`} className="pj-panel">
              {tab === 'overview' && (
                <Overview project={project} waiting={waiting} chats={chats} memory={memory} archived={archived} starting={starting} loadingModels={modelsLoading} onStart={startChat} onTab={setTab} lang={i18n.language} t={t} />
              )}
              {tab === 'files' && <ProjectFiles project={project} archived={archived} />}
              {tab === 'chats' && <ProjectChats chats={chats} />}
              {tab === 'agents' && <ProjectAgents project={project} archived={archived} addToast={addToast} onChanged={() => load({ quiet: true })} />}
              {tab === 'connections' && <ProjectConnections project={project} archived={archived} />}
              {tab === 'automations' && <ProjectAutomations project={project} archived={archived} onRestore={() => act('restore')} />}
              {tab === 'memory' && <ProjectMemory project={project} memory={memory} disabled={archived} addToast={addToast} onChanged={() => load({ quiet: true })} />}
              {tab === 'skills' && <ProjectSkills project={project} archived={archived} addToast={addToast} />}
              {tab === 'tools' && <ProjectTools project={project} archived={archived} addToast={addToast} onSaved={setProject} />}
            </div>
          </>
        )}
      </div>

      {sharing && shareClient && (
        <ShareDialog
          client={shareClient} roles={shareRoles} allowEveryone resource={sharing} sharing={shares.available}
          intro={t('share.dialogIntro')} testId="project-share-dialog"
          onChanged={loadShares}
          onLost={() => { setSharing(null); addToast(t('share.lost', { ns: 'workspace' }), 'error'); loadShares() }}
          close={() => setSharing(null)}
        />
      )}
      <ConfirmDialog
        open={Boolean(confirmDelete)}
        danger
        title={t('delete.title', { name: shortName(project.name) })}
        message={confirmDelete && <DeleteEffects counts={confirmDelete} t={t} />}
        confirmLabel={t('actions.delete')}
        cancelLabel={t('common:actions.cancel')}
        onConfirm={remove}
        onCancel={() => setConfirmDelete(null)}
      />
      {editing && (
        <ProjectSheet
          project={project}
          onClose={() => setEditing(false)}
          onSaved={(p) => {
            setEditing(false)
            setProject(p)
            addToast(t('toasts.saved', { name: p.name }), 'success')
          }}
        />
      )}
    </Page>
  )
}

// Start here, the recent chats, the counts and a look at the memory.
function Overview({ project, waiting, chats, memory, archived, starting, loadingModels, onStart, onTab, lang, t }) {
  const recent = chats.slice(0, 3)
  const topics = memoryTopicCount(memory)
  const firstLine = (memory.index || '').split('\n').filter(l => l.trim() && !l.trim().startsWith('#')).slice(0, 4).join(' ').replace(/[#*_`>-]/g, '').trim()
  return (
    <div className="pj-overview" data-testid="project-overview">
      <div className="pj-overview__main">
        {waiting > 0 && (
          <section className="pj-section" data-testid="project-waiting">
            <h2 className="pj-h2">{t('overview.waiting')}</h2>
            <div className="pj-card pj-card--row">
              <p>{t('overview.waitingBody', { count: waiting })}</p>
              <Button to="/app/chats/proposals" data-testid="project-waiting-link">{t('overview.waitingLink')}</Button>
            </div>
          </section>
        )}
        <section className="pj-section">
          <h2 className="pj-h2">{t('overview.start')}</h2>
          <div className="pj-card pj-card--row">
            <p>{t('overview.startBody')}</p>
            <Button variant="primary" icon="chat" busy={starting} disabled={archived || loadingModels} onClick={onStart} data-testid="project-start-chat">{t('actions.startChat')}</Button>
          </div>
        </section>
        <section className="pj-section">
          <div className="pj-head">
            <h2 className="pj-h2">{t('overview.recent')}</h2>
            {chats.length > 3 && <button type="button" className="pj-link" onClick={() => onTab('chats')}>{t('actions.allChats', { count: chats.length })}</button>}
          </div>
          {recent.length === 0 ? <p className="pj-muted" data-testid="project-no-chats">{t('overview.noChats')}</p> : <ChatRows chats={recent} />}
        </section>
      </div>
      <aside className="pj-overview__side">
        <section className="pj-section">
          <h2 className="pj-h2">{t('overview.stats')}</h2>
          <div className="pj-stats">
            <div className="pj-stat" data-testid="project-stat-chats"><strong>{project.chat_count || chats.length}</strong><span>{t('overview.statChats')}</span></div>
            <div className="pj-stat" data-testid="project-stat-agents"><strong>{project.agent_count || 0}</strong><span>{t('overview.statAgents')}</span></div>
            <div className="pj-stat" data-testid="project-stat-memory"><strong>{topics}</strong><span>{t('overview.statMemory')}</span></div>
            <div className="pj-stat" data-testid="project-stat-tools"><strong>{defaultsCount(project.defaults)}</strong><span>{t('overview.statTools')}</span></div>
          </div>
        </section>
        <section className="pj-section">
          <div className="pj-head">
            <h2 className="pj-h2">{t('overview.memory')}</h2>
            <button type="button" className="pj-link" onClick={() => onTab('memory')}>{t('actions.openMemory')}</button>
          </div>
          <div className="pj-card">
            <p className="pj-mono">{t('memory.index')}</p>
            {firstLine ? <p className="pj-clamp">{firstLine}</p> : <p className="pj-muted">{t('overview.memoryEmpty')}</p>}
          </div>
        </section>
      </aside>
    </div>
  )
}

// What deleting a project touches, as the Delete dialog says it.
function DeleteEffects({ counts, t }) {
  const lines = []
  if (counts.agents > 0) lines.push(t('delete.agents', { n: counts.agents }))
  if (counts.triggers > 0) lines.push(t('delete.triggers', { n: counts.triggers }))
  if (counts.chats > 0) lines.push(t('delete.chats', { n: counts.chats }))
  return (
    <div data-testid="project-delete-effects">
      <p>{t('delete.body')}</p>
      {lines.length > 0
        ? <ul>{lines.map(l => <li key={l}>{l}</li>)}</ul>
        : <p>{t('delete.nothing')}</p>}
    </div>
  )
}

// A name of up to 120 characters can be one word; the dialog title cuts it so
// the dialog keeps its width. The full name is in the page behind it.
const shortName = (name) => (name.length > 60 ? `${name.slice(0, 59)}…` : name)
