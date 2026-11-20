/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatProjectsApi, chatsApi } from '../utils/api'
import { CAP_CHAT } from '../utils/capabilities'
import { useModels } from '../hooks/useModels'
import { usePolling } from '../hooks/usePolling'
import ActionMenu from '../components/ActionMenu'
import Icon from '../components/Icon'
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SkeletonRows, StatusChip } from '../components/ui'
import { SharedAccess, unavailableText } from '../components/projects/SharedProjects'
import { chatPath } from '../components/projects/ProjectChats'
import { readChatStart, readLastModel } from '../utils/lastModel'
import { MEMORY_INDEX, isProjectsOff, pickStartModel, whenAgo, withPulled, wrapKey } from '../utils/projects'
import { decodeSharedRef, sharedAccess, sharedUsable, memorySnippet } from '../utils/sharedProjects'
import { renderSharedMarkdown } from '../utils/sharedMarkdown'
import './projects.css'

const TABS = ['overview', 'memory']
// The most recent chats the Pull into a chat menu offers next to New chat.
const RECENT_CHATS = 5

// A project that another person shares with you: its facts, a read-only look at
// its memory, and the way to pull it into a chat. Everything here is the
// owner's text, drawn as text. The page has no way to change the project.
export default function SharedProjectPage() {
  const { ref: param } = useParams()
  const ref = decodeSharedRef(param)
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t, i18n } = useTranslation(['projects', 'common'])
  const [params, setParams] = useSearchParams()
  const tab = TABS.includes(params.get('tab')) ? params.get('tab') : 'overview'
  const [project, setProject] = useState(null)
  const [memory, setMemory] = useState({ index: '', files: [] })
  // The memory read can fail on its own: that is said, never shown as an empty memory.
  const [memoryFailed, setMemoryFailed] = useState(false)
  const [state, setState] = useState(ref ? 'loading' : 'missing') // loading | ready | error | off | missing
  const [error, setError] = useState(null)
  const [recent, setRecent] = useState([])
  const [menuAt, setMenuAt] = useState(null)
  const [pulling, setPulling] = useState(false)
  const { models, loading: modelsLoading } = useModels(CAP_CHAT)
  const tabRefs = useRef({})
  const loaded = useRef(false)

  // The project and its memory. A poll that fails keeps what is shown; a 404
  // means the share was removed, which the page says in its own words.
  const current = useRef(ref)
  current.current = ref
  // Another project (the :ref changed) starts from nothing: no data, no error,
  // not loaded. An answer that comes back for the old ref is dropped.
  useEffect(() => {
    loaded.current = false
    setProject(null)
    setMemory({ index: '', files: [] })
    setMemoryFailed(false)
    setError(null)
    setState(ref ? 'loading' : 'missing')
  }, [ref])
  const load = useCallback(async () => {
    if (!ref) return
    const stale = () => current.current !== ref
    try {
      const p = await chatProjectsApi.sharedGet(ref)
      let m = null
      try { m = await chatProjectsApi.sharedMemory(ref) } catch { m = null }
      if (stale()) return
      setProject(p)
      setMemory(m ? { index: m.index || '', files: m.files || [] } : prev => prev)
      setMemoryFailed(m === null)
      setState('ready')
      loaded.current = true
    } catch (err) {
      if (stale()) return
      if (isProjectsOff(err)) setState('off')
      else if (err?.status === 404) { setProject(null); setState('missing') }
      else if (!loaded.current) { setError(err); setState('error') }
    }
  }, [ref])
  const { refetch } = usePolling(load, 8000)

  // The recent chats the menu can pull into: best effort, the person's own.
  useEffect(() => {
    let live = true
    chatsApi.list(RECENT_CHATS + 3, 'chat', 'person')
      .then((r) => { if (live) setRecent((r?.conversations || []).filter(c => c.kind !== 'subagent').slice(0, RECENT_CHATS)) })
      .catch(() => {})
    return () => { live = false }
  }, [])

  useEffect(() => {
    tabRefs.current[tab]?.scrollIntoView?.({ inline: 'center', block: 'nearest' })
  }, [tab, state])

  const setTab = (next) => setParams(next === 'overview' ? {} : { tab: next }, { replace: true })
  const onTabKey = (e) => {
    const to = wrapKey(e.key, TABS.indexOf(tab), TABS.length)
    if (to < 0) return
    e.preventDefault()
    setTab(TABS[to])
    tabRefs.current[TABS[to]]?.focus()
  }

  // Pulls the project into a new chat, or into one the person already has.
  const pull = async (chat) => {
    setMenuAt(null)
    setPulling(true)
    try {
      let target = chat
      let pulled = []
      if (!target) {
        const model = pickStartModel(models, readChatStart(), readLastModel(CAP_CHAT))
        if (!model) { addToast(t('toasts.noModel'), 'warning'); setPulling(false); return }
        target = await chatsApi.create(model)
      } else {
        pulled = (await chatsApi.get(chat.id))?.pulled_projects || []
      }
      try {
        await chatProjectsApi.setChatPulled(target.id, withPulled(pulled, ref, true))
      } catch (err) {
        addToast(t('toasts.pullFailed', { message: err.message }), 'error')
        if (chat) { setPulling(false); return }
      }
      navigate(chatPath(target))
    } catch (err) {
      addToast(t('toasts.chatFailed', { message: err.message }), 'error')
      setPulling(false)
    }
  }

  const html = useMemo(() => renderSharedMarkdown(memory.index || ''), [memory.index])

  const back = <Link className="pj-back" to="/app/projects"><Icon name="arrow-left" />{t('back')}</Link>
  if (state === 'off') return <Page width="full"><PageHeader title={t('title')} /><div role="note"><EmptyState variant="unavailable" title={t('share.off')} /></div></Page>
  if (state === 'missing') {
    return (
      <Page width="full" className="pj-page">
        {back}
        <EmptyState variant="no-results" title={t('share.missingTitle')} body={t('share.missingBody')} testId="shared-project-missing" actions={<Button to="/app/projects">{t('share.backToProjects')}</Button>} />
      </Page>
    )
  }
  if (state === 'error') {
    return (
      <Page width="full" className="pj-page">
        {back}
        <LoadError title={t('share.loadError')} error={error} onRetry={() => { setState('loading'); refetch() }} />
      </Page>
    )
  }
  if (state === 'loading' || !project) return <Page width="full" className="pj-page"><SkeletonRows count={3} /></Page>

  const usable = sharedUsable(project)
  const write = sharedAccess(project.mode) === 'write'
  const archived = project.status === 'archived'
  const menu = [
    { key: 'new', icon: 'plus', label: t('share.pullNew'), onClick: () => pull(null) },
    ...(recent.length > 0 ? [{ divider: true }, { type: 'badge', label: t('share.pullRecent') }] : []),
    ...recent.map(c => ({ key: c.id, icon: 'chat', label: c.title || t('chats.untitled'), onClick: () => pull(c) })),
  ]

  return (
    <Page width="full" className="pj-page">
      <div className="pj-home" data-testid="shared-project">
        {back}
        <PageHeader
          title={project.name}
          supporting={project.description}
          actions={(
            <Button
              variant="primary"
              icon="layers"
              busy={pulling}
              disabled={!usable || modelsLoading}
              aria-haspopup="menu"
              aria-expanded={menuAt ? true : undefined}
              onClick={(e) => { const r = e.currentTarget.getBoundingClientRect(); setMenuAt({ x: r.left, y: r.bottom + 4, returnFocus: e.currentTarget }) }}
              data-testid="shared-pull"
            >{t('share.pull')}</Button>
          )}
        />
        <div className="pj-meta">
          {usable
            ? <><StatusChip tone="ok" label={t('status.active')} /><SharedAccess mode={project.mode} /></>
            : <StatusChip tone="warn" icon="alert-circle" label={t('share.unavailable')} />}
        </div>

        {usable
          ? (
            <Callout tone="info" icon={write ? 'edit' : 'eye'} title={t('share.sharedBy', { name: project.owner_name })} testId="shared-banner">
              {t(write ? 'share.bannerWrite' : 'share.bannerRead')}
            </Callout>
          )
          : (
            <Callout tone="warn" title={t(archived ? 'share.archivedTitle' : 'share.brokenTitle')} testId="shared-unavailable">
              {archived ? t('share.archivedBody', { name: project.owner_name }) : unavailableText(project, t)}
            </Callout>
          )}

        <div className="pj-tabs" role="tablist" aria-label={t('tabs.label')} onKeyDown={onTabKey}>
          {TABS.map(k => (
            <button
              key={k}
              ref={el => { tabRefs.current[k] = el }}
              type="button"
              role="tab"
              id={`shared-tab-${k}`}
              aria-selected={tab === k}
              aria-controls={`shared-panel-${k}`}
              tabIndex={tab === k ? 0 : -1}
              className="pj-tab"
              data-testid={`shared-tab-${k}`}
              onClick={() => setTab(k)}
            >
              {t(`tabs.${k}`)}
            </button>
          ))}
        </div>
        <div role="tabpanel" id={`shared-panel-${tab}`} aria-labelledby={`shared-tab-${tab}`} className="pj-panel">
          {tab === 'overview' && <Overview project={project} memory={memory} usable={usable} write={write} onMemory={() => setTab('memory')} memoryFailed={memoryFailed} onRetry={refetch} t={t} />}
          {tab === 'memory' && <SharedMemory memory={memory} html={html} failed={memoryFailed} onRetry={refetch} t={t} />}
        </div>
      </div>
      {menuAt && <ActionMenu at={menuAt} onClose={() => setMenuAt(null)} items={menu} ariaLabel={t('share.pull')} />}
    </Page>
  )
}

function Overview({ project, memory, usable, write, onMemory, memoryFailed, onRetry, t }) {
  const firstLine = memorySnippet(memory.index)
  return (
    <div className="pj-overview" data-testid="shared-overview">
      <div className="pj-overview__main">
        <section className="pj-section">
          <h2 className="pj-h2">{t('share.useTitle')}</h2>
          <div className="pj-card">
            <p data-testid="shared-use-body">{usable ? t(write ? 'share.useBodyWrite' : 'share.useBodyRead') : t(project.status === 'archived' ? 'share.useBodyArchived' : 'share.useBodyBroken')}</p>
          </div>
        </section>
        <section className="pj-section">
          <h2 className="pj-h2">{t('share.aboutTitle')}</h2>
          <dl className="pj-facts" data-testid="shared-facts">
            <div className="pj-fact"><dt>{t('share.factOwner')}</dt><dd>{project.owner_name}</dd></div>
            <div className="pj-fact">
              <dt>{t('share.factAccess')}</dt>
              <dd>{usable ? <SharedAccess mode={project.mode} /> : <StatusChip tone="warn" icon="alert-circle" label={t('share.unavailable')} />}</dd>
            </div>
            <div className="pj-fact"><dt>{t('share.factCovers')}</dt><dd>{t('share.coversValue')}</dd></div>
            <div className="pj-fact"><dt>{t('share.factMemory')}</dt><dd><StatusChip tone="idle" icon="lock" label={t('share.memoryReadOnly')} /></dd></div>
          </dl>
        </section>
      </div>
      <aside className="pj-overview__side">
        <section className="pj-section">
          <div className="pj-head">
            <h2 className="pj-h2">{t('overview.memory')}</h2>
            <button type="button" className="pj-link" onClick={onMemory}>{t('actions.openMemory')}</button>
          </div>
          <div className="pj-card">
            <p className="pj-mono">{t('memory.index')}</p>
            {memoryFailed
              ? <p className="pj-muted" data-testid="shared-memory-failed">{t('load.memory')}</p>
              : firstLine ? <p className="pj-clamp">{firstLine}</p> : <p className="pj-muted">{t('overview.memoryEmpty')}</p>}
          </div>
        </section>
      </aside>
    </div>
  )
}

// The Memory tab: the index drawn as Markdown and the names of the topic files.
// Nothing here can be edited.
function SharedMemory({ memory, html, failed, onRetry, t }) {
  if (failed) return <div className="pj-stack"><LoadError title={t('load.memory')} onRetry={onRetry} testId="shared-memory-failed" /></div>
  const topics = (memory.files || []).filter(name => name !== MEMORY_INDEX)
  return (
    <div className="pj-stack" data-testid="shared-memory">
      <section className="pj-section">
        <h2 className="pj-h2">{t('memory.title')}</h2>
        <p className="pj-note">{t('share.memoryNote')}</p>
        <div className="pj-card">
          <h3 className="pj-mono">{t('memory.index')}</h3>
          {memory.index
            ? <div className="pj-md cx-prose" data-testid="shared-memory-index" dangerouslySetInnerHTML={{ __html: html }} />
            : <p className="pj-muted">{t('memory.empty')}</p>}
        </div>
      </section>
      <section className="pj-section">
        <h2 className="pj-h2">{t('memory.topics')}</h2>
        {topics.length === 0
          ? <p className="pj-muted">{t('memory.noTopics')}</p>
          : (
            <ul className="pj-list">
              {topics.map(name => (
                <li key={name} className="pj-line" data-testid="shared-memory-file"><span className="pj-mono">{name}</span></li>
              ))}
            </ul>
          )}
      </section>
    </div>
  )
}
