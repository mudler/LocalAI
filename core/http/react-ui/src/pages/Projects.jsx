/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatProjectsApi } from '../utils/api'
import Icon from '../components/Icon'
import { Button, EmptyState, LoadError, Page, PageHeader, SearchField, Segmented, SkeletonRows, StatusChip } from '../components/ui'
import ProjectSheet from '../components/projects/ProjectSheet'
import SharedProjects from '../components/projects/SharedProjects'
import { usePolling } from '../hooks/usePolling'
import { keepIfSame } from '../utils/sameData'
import { deleteProjectWithUndo, usePendingProjectDeletes } from '../components/projects/projectUndo'
import { filterProjects, isProjectsOff, projectCounts, projectFolderLink, sortProjects, whenAgo } from '../utils/projects'
import './projects.css'

export const projectPath = (id) => `/app/projects/${encodeURIComponent(id)}`

// The chip of a project's state: active is ok, archived is quiet, broken is an error.
export function ProjectStatus({ status }) {
  const { t } = useTranslation('projects')
  const label = t(`status.${status}`, { defaultValue: status })
  if (status === 'broken') return <StatusChip status="failed" label={label} />
  return <StatusChip tone={status === 'active' ? 'ok' : 'idle'} label={label} />
}

export default function Projects() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { t, i18n } = useTranslation(['projects', 'common'])
  const [projects, setProjects] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState(null)
  const [off, setOff] = useState('')
  const [query, setQuery] = useState('')
  const [status, setStatus] = useState('all')
  const [creating, setCreating] = useState(false)
  const [shared, setShared] = useState([])
  const deleting = usePendingProjectDeletes()

  // A failed read is said in the list's place, never as a toast and never as
  // an empty list. A later poll that fails keeps the rows it already has.
  const load = useCallback(async () => {
    try {
      const list = await chatProjectsApi.list()
      setProjects(prev => keepIfSame(prev, list.projects || []))
      setOff('')
      setError(null)
      setLoaded(true)
    } catch (err) {
      if (isProjectsOff(err)) setOff(err.body.error.message)
      else setError(err)
    }
  }, [])

  usePolling(load, 5000)

  // The projects shared with the person. Best effort and quiet: a failed read
  // hides the section (a poll that fails keeps the rows it has), and it never
  // raises an error or a toast. It polls like the list but pauses while the
  // tab is hidden.
  const loadShared = useCallback(async () => {
    try {
      const r = await chatProjectsApi.shared()
      setShared(prev => keepIfSame(prev, r?.projects || []))
    } catch { /* the section stays as it was, or hidden */ }
  }, [])
  usePolling(loadShared, 5000)

  const rows = useMemo(() => projects.filter(p => !deleting.has(p.id)), [projects, deleting])
  const counts = useMemo(() => projectCounts(rows), [rows])
  const shown = useMemo(() => sortProjects(filterProjects(rows, { status, query })), [rows, status, query])
  const sharedShown = useMemo(() => sortProjects(filterProjects(shared, { query })), [shared, query])
  // The empty state is for a person with no project of their own and none
  // shared with them.
  const empty = loaded && !error && rows.length === 0 && shared.length === 0
  const newProject = <Button variant="primary" icon="plus" onClick={() => setCreating(true)} data-testid="project-new">{t('actions.new')}</Button>

  const options = ['all', 'active', 'archived', 'broken'].map(id => ({ id, label: t(`filters.${id}`), count: counts[id] }))
  const clear = () => { setQuery(''); setStatus('all') }

  return (
    <Page width="full" className="pj-page">
      <PageHeader title={t('title')} supporting={t('subtitle')} actions={!off && !empty && loaded ? newProject : undefined} />

      {off ? (
        <div role="note"><EmptyState variant="unavailable" title={off} /></div>
      ) : error && !loaded ? (
        <LoadError title={t('load.projects')} error={error} onRetry={load} />
      ) : !loaded ? (
        <SkeletonRows count={3} />
      ) : empty ? (
        <EmptyState
          mascot="welcome"
          testId="projects-empty"
          title={t('empty.title')}
          body={t('empty.body')}
          actions={newProject}
        />
      ) : (
        <div className="pj-launch">
          {error && <LoadError title={t('load.projectsStale')} error={error} onRetry={load} />}
          <div className="pj-tools">
            <SearchField value={query} onChange={setQuery} label={t('search.label')} />
            <Segmented label={t('filters.label')} value={status} onChange={setStatus} options={options} itemTestId="projects-filter-" />
          </div>
          {rows.length === 0 ? (
            <p className="pj-muted" data-testid="projects-none-own">{t('share.noneOwn')}</p>
          ) : shown.length === 0 ? (
            <EmptyState
              variant="no-results"
              title={t('empty.noMatch')}
              body={t('empty.noMatchBody')}
              actions={<Button variant="ghost" size="sm" onClick={clear}>{t('empty.clear')}</Button>}
            />
          ) : (
            <ul className="pj-rows" data-testid="projects-list">
              {shown.map(p => (
                <ProjectRow key={p.id} project={p} t={t} lang={i18n.language} onRemove={() => deleteProjectWithUndo(p, { broken: true, addToast, t, onDeleted: load, onFailed: load })} />
              ))}
            </ul>
          )}
          <SharedProjects projects={sharedShown} />
        </div>
      )}

      {creating && (
        <ProjectSheet
          onClose={() => setCreating(false)}
          onSaved={(p) => {
            setCreating(false)
            addToast(t('toasts.saved', { name: p.name }), 'success')
            navigate(projectPath(p.id))
          }}
        />
      )}
    </Page>
  )
}

// One project: a tile, its name and line, its state, the chats and the time,
// and Open. A broken project says why and offers Remove instead.
function ProjectRow({ project: p, t, lang, onRemove }) {
  const broken = p.status === 'broken'
  return (
    <li className={`pj-row${broken ? ' pj-row--broken' : ''}`} data-testid="project-row" data-project={p.name} data-status={p.status}>
      <span className="pj-tile" aria-hidden="true"><Icon name="layers" /></span>
      <div className="pj-row__main">
        {broken
          ? <span className="pj-row__name">{p.name}</span>
          : <Link className="pj-row__name" to={projectPath(p.id)}>{p.name}</Link>}
        {p.description && <span className="pj-row__desc">{p.description}</span>}
        {broken && (
          <span className="pj-row__error" role="note" data-testid="project-row-error">
            <Icon name="alert-circle" />{p.error || t('broken.fallback')}
          </span>
        )}
      </div>
      <div className="pj-row__meta">
        <ProjectStatus status={p.status} />
        <span className="pj-muted"><Icon name="chat" />{t('row.chats', { count: p.chat_count || 0 })}</span>
        {p.updated && <span className="pj-muted"><Icon name="clock" />{whenAgo(p.updated, lang)}</span>}
      </div>
      <div className="pj-row__end">
        {broken
          ? (
            <>
              <Button size="sm" to={projectFolderLink(p)} aria-label={t('actions.openInFilesNamed', { name: p.name })} data-testid="project-row-files">{t('actions.openInFiles')}</Button>
              <Button variant="danger-ghost" size="sm" aria-label={t('actions.removeNamed', { name: p.name })} onClick={onRemove}>{t('actions.remove')}</Button>
            </>
          )
          : <Button size="sm" to={projectPath(p.id)} aria-label={`${t('actions.open')} ${p.name}`}>{t('actions.open')}</Button>}
      </div>
    </li>
  )
}
