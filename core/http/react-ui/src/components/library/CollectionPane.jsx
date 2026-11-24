import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { agentCollectionsApi } from '../../utils/api'
// eslint-disable-next-line no-unused-vars
import { SharedByBadge } from '../share/ShareBadges'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, LoadError, SearchField, SkeletonRows, StatusChip } from '../ui'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import { AddToMenu, PassageList, UsedByStrip } from './LibraryBits'
import { entryName, normaliseSource } from '../../utils/library'
import { matchesQuery } from '../../utils/libraryList'

const DEFAULT_PASSAGES = 10
// How many files the list draws at first, and per press of "Show more".
const FILES_PAGE = 50
// A filter box is worth its row once the list is longer than this.
const FILTER_FROM = 8

function when(ts) {
  try { return new Date(ts).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) } catch { return '' }
}

const PATHS = [
  ['POST', '/api/collections/{name}/upload', 'upload'],
  ['GET', '/api/collections/{name}/entries', 'entries'],
  ['POST', '/api/collections/{name}/search', 'search'],
  ['GET', '/api/collections/{name}/sources', 'sources'],
  ['POST', '/api/collections/{name}/reset', 'reset'],
]

export default function CollectionPane({ t, name, userId, users, status, agents, busy, addToast, onBack, onReset, onAdd, onRemove, onTry, setConfirmDialog, shared, onShare, shareLine }) {
  // A collection shared with the user is read only, except that an editor
  // adds and removes files. Sources, reset and sharing stay the owner's.
  const own = !userId && !shared
  const canWrite = !shared || shared.role === 'editor'
  const ownerOnly = !shared
  const [entries, setEntries] = useState([])
  const [sources, setSources] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [entriesError, setEntriesError] = useState(null)
  const [sourcesError, setSourcesError] = useState(null)
  const [addOpen, setAddOpen] = useState(false)
  const [uploadFile, setUploadFile] = useState(null)
  const [attempts, setAttempts] = useState([])
  const [newSourceUrl, setNewSourceUrl] = useState('')
  const [newSourceInterval, setNewSourceInterval] = useState('')
  const [addingSource, setAddingSource] = useState(false)
  const [query, setQuery] = useState('')
  const [maxResults, setMaxResults] = useState(DEFAULT_PASSAGES)
  const [searching, setSearching] = useState(false)
  const [answer, setAnswer] = useState(null)
  const [view, setView] = useState(null)
  const [fileQuery, setFileQuery] = useState('')
  const [fileLimit, setFileLimit] = useState(FILES_PAGE)
  const nextId = useRef(0)
  const fileInput = useRef(null)
  const unused = own && status === 'ready' && users.length === 0

  const fetchEntries = useCallback(async () => {
    try {
      const data = await agentCollectionsApi.entries(name, userId)
      setEntries(Array.isArray(data.entries) ? data.entries : [])
      setEntriesError(null)
      return true
    } catch (err) {
      setEntriesError(err)
      return false
    }
  }, [name, userId])

  const fetchSources = useCallback(async () => {
    try {
      const data = await agentCollectionsApi.sources(name, userId)
      setSources((Array.isArray(data.sources) ? data.sources : []).map(normaliseSource))
      setSourcesError(null)
      return true
    } catch (err) {
      setSourcesError(err)
      return false
    }
  }, [name, userId])

  useEffect(() => {
    let live = true
    Promise.all([fetchEntries(), fetchSources()]).then(() => {
      if (live) setLoaded(true)
    })
    return () => { live = false }
  }, [fetchEntries, fetchSources])

  const upload = async (file, existingId) => {
    const id = existingId ?? ++nextId.current
    setAttempts(prev => [...prev.filter(a => a.id !== id), { id, name: file.name, file, phase: 'uploading' }])
    try {
      const formData = new FormData()
      formData.append('file', file)
      await agentCollectionsApi.upload(name, formData, userId)
      setAttempts(prev => prev.filter(a => a.id !== id))
      addToast(t('collections:toasts.uploaded', { file: file.name }), 'success')
      fetchEntries()
    } catch (err) {
      setAttempts(prev => prev.map(a => (a.id === id ? { ...a, phase: 'failed', message: err.message } : a)))
    }
  }

  const handleUpload = (e) => {
    e.preventDefault()
    if (!uploadFile) return
    const file = uploadFile
    setUploadFile(null)
    if (fileInput.current) fileInput.current.value = ''
    upload(file)
  }

  const handleSearch = async (e) => {
    e.preventDefault()
    const q = query.trim()
    if (!q) return
    setSearching(true)
    try {
      const data = await agentCollectionsApi.search(name, q, maxResults, userId)
      setAnswer({ query: q, results: Array.isArray(data.results) ? data.results : [], error: null })
    } catch (err) {
      setAnswer({ query: q, results: [], error: err.message })
    } finally {
      setSearching(false)
    }
  }

  const handleAddSource = async (e) => {
    e.preventDefault()
    if (!newSourceUrl.trim()) return
    setAddingSource(true)
    try {
      await agentCollectionsApi.addSource(name, newSourceUrl, newSourceInterval || undefined, userId)
      addToast(t('collections:toasts.sourceAdded'), 'success')
      setNewSourceUrl('')
      setNewSourceInterval('')
      fetchSources()
    } catch (err) {
      addToast(t('collections:toasts.sourceFailed', { message: err.message }), 'error')
    } finally {
      setAddingSource(false)
    }
  }

  const removeSource = (url) => {
    setConfirmDialog({
      title: t('collections:removeSource.title'),
      message: t('collections:removeSource.message'),
      confirmLabel: t('collections:removeSource.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await agentCollectionsApi.removeSource(name, url, userId)
          addToast(t('collections:toasts.sourceRemoved'), 'success')
          fetchSources()
        } catch (err) {
          addToast(t('collections:toasts.sourceRemoveFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  const openEntry = async (entry) => {
    setView({ entry, loading: true, data: null })
    try {
      const data = await agentCollectionsApi.entryContent(name, entry, userId)
      setView({ entry, loading: false, data })
    } catch (err) {
      addToast(t('collections:toasts.contentFailed', { message: err.message }), 'error')
      setView(null)
    }
  }

  const deleteEntry = (entry) => {
    setConfirmDialog({
      title: t('collections:deleteEntry.title'),
      message: t('collections:deleteEntry.message'),
      confirmLabel: t('collections:deleteEntry.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await agentCollectionsApi.deleteEntry(name, entry, userId)
          addToast(t('collections:toasts.entryDeleted'), 'success')
          fetchEntries()
        } catch (err) {
          addToast(t('collections:toasts.entryDeleteFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  useEffect(() => {
    if (!view) return undefined
    const onKey = e => { if (e.key === 'Escape') setView(null) }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [view])

  const failed = attempts.filter(a => a.phase === 'failed').length
  // The file list is searched by name; a long one is drawn a page at a time.
  const matching = useMemo(
    () => entries.map(entry => ({ entry, label: entryName(entry) })).filter(({ label }) => matchesQuery(fileQuery, label)),
    [entries, fileQuery],
  )
  const sub = loaded && !entriesError && !sourcesError
    ? t('collections:pane.counts', { files: entries.length, sources: sources.length })
    : ''

  return (
    <>
      <Button variant="ghost" size="sm" icon="arrow-left" className="lib-back" onClick={onBack}>
        {t('collections:pane.back')}
      </Button>

      <div className="lib-pane__head">
        <div className="lib-pane__title">
          <h2 data-testid="collection-title">{shared?.title || name}</h2>
          <p className="lib-pane__sub" data-testid="collection-counts">{sub}</p>
          {shared && <SharedByBadge entry={{ role: shared.role, owner: shared.owner, everyone: shared.everyone }} roleLabel={t(`collections:share.roles.${shared.role === 'editor' ? 'editor' : 'viewer'}`)} testId="collection-shared-by" />}
          {shareLine && <SharedByBadge entry={{ role: 'owner', sharedWith: shareLine.shared_with }} hideRole testId="collection-share-line" />}
        </div>
        <div className="lib-pane__acts">
          {failed > 0 && <StatusChip tone="error" testId="collection-failed-badge">{t('collections:pane.failed', { count: failed })}</StatusChip>}
          {onShare && (
            <Button size="sm" icon="share" onClick={onShare} aria-label={t('collections:share.button', { name })} data-testid="collection-share">
              {t('collections:share.label')}
            </Button>
          )}
          {canWrite && (
            <Button variant="primary" size="sm" icon="plus" aria-expanded={addOpen} onClick={() => setAddOpen(v => !v)} data-testid="add-source-toggle">
              {t(ownerOnly ? 'collections:pane.addSource' : 'collections:pane.addFile')}
            </Button>
          )}
          {ownerOnly && (
            <Button variant="danger-ghost" size="sm" icon="eraser" onClick={onReset} title={t('collections:actions.resetCollection')}>
              {t('collections:actions.reset')}
            </Button>
          )}
        </div>
      </div>

      {own && (
        <>
          <UsedByStrip users={users} status={status} onRemove={onRemove} busy={busy}>
            <AddToMenu name={name} agents={agents} status={status} onAdd={onAdd} busy={busy} />
          </UsedByStrip>
          {unused && (
            <Callout
              icon="info"
              testId="collection-not-used"
              actions={(
                <Button size="sm" icon="flask" onClick={onTry} data-testid="collection-try">
                  {t('library:tryInContext')}
                </Button>
              )}
            >
              {t('library:notUsed.collection')}
            </Callout>
          )}
        </>
      )}

      {shared && (
        <p className="lib-muted lib-fine" data-testid="collection-shared-note">
          {t(canWrite ? 'collections:share.noteEditor' : 'collections:share.noteViewer')}
        </p>
      )}

      {addOpen && canWrite && (
        <div className="lib-adding" data-testid="add-source-panel">
          <form className="lib-adding__row" onSubmit={handleUpload}>
            <div className="dk-field">
              <label className="dk-label" htmlFor="upload-file">{t('collections:add.file')}</label>
              <input id="upload-file" ref={fileInput} className="dk-input" type="file" onChange={(e) => setUploadFile(e.target.files[0] || null)} />
            </div>
            <Button type="submit" icon="upload" disabled={!uploadFile}>
              {t('collections:add.upload')}
            </Button>
          </form>
          {ownerOnly && <form className="lib-adding__row" onSubmit={handleAddSource}>
            <div className="dk-field">
              <label className="dk-label" htmlFor="source-url">{t('collections:add.url')}</label>
              <input id="source-url" className="dk-input" type="text" value={newSourceUrl} onChange={(e) => setNewSourceUrl(e.target.value)} placeholder="https://example.com/data" />
            </div>
            <div className="dk-field lib-narrow">
              <label className="dk-label" htmlFor="source-interval">{t('collections:add.interval')}</label>
              <input id="source-interval" className="dk-input" type="number" min="1" step="1" value={newSourceInterval} onChange={(e) => setNewSourceInterval(e.target.value)} placeholder={t('collections:add.intervalPlaceholder')} />
            </div>
            <Button type="submit" icon="plus" busy={addingSource} disabled={!newSourceUrl.trim()}>
              {t('collections:add.addUrl')}
            </Button>
          </form>}
        </div>
      )}

      {attempts.length > 0 && (
        <section className="lib-section" aria-labelledby="adding-h">
          <h3 className="lib-section__title" id="adding-h">{t('collections:entries.adding')}</h3>
          <ul className="lib-ledger" data-testid="uploads-list">
              {attempts.map(a => (
                <li key={a.id} className="lib-line" data-state={a.phase}>
                  <span className="lib-line__lead"><Icon name={a.phase === 'failed' ? 'alert-circle' : 'cloud-upload'} /></span>
                  <span className="lib-line__main">
                    <span className="lib-line__name">{a.name}</span>
                    <span className="lib-line__meta">{a.phase === 'failed' ? t('collections:entries.uploadFailed') : t('collections:entries.indexing')}</span>
                  </span>
                  <span className="lib-line__acts">
                    {a.phase === 'failed' && (
                      <>
                        <Button size="sm" icon="refresh" onClick={() => upload(a.file, a.id)}>
                          {t('collections:entries.retry')}
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setAttempts(prev => prev.filter(x => x.id !== a.id))}>
                          {t('collections:entries.dismiss')}
                        </Button>
                      </>
                    )}
                  </span>
                  {a.phase === 'failed' && <p className="lib-line__err" role="alert" data-testid="upload-error">{a.message}</p>}
                  {a.phase === 'uploading' && (
                    <div className="dk-progress lib-line__prog" role="progressbar" aria-label={t('collections:entries.indexing')} data-indeterminate><span className="dk-progress-bar" /></div>
                  )}
                </li>
              ))}
          </ul>
        </section>
      )}

      <section className="lib-section" aria-labelledby="ask-h">
        <h3 className="lib-section__title" id="ask-h">{t('collections:ask.title')}</h3>
        <form className="lib-ask" onSubmit={handleSearch}>
          <span className="dk-input-icon">
            <Icon name="search" className="dk-icon" />
            <input
              id="search-query"
              className="dk-input"
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('collections:ask.placeholder')}
              aria-label={t('collections:ask.label')}
            />
          </span>
          <Button type="submit" variant="primary" busy={searching} disabled={!query.trim()}>
            {t('collections:ask.search')}
          </Button>
        </form>
        <p className="lib-muted lib-fine">{t('collections:ask.alone')}</p>
        <details className="lib-disc">
          <summary><Icon name="chevron-right" />{t('collections:ask.tune')}</summary>
          <div className="lib-disc__body lib-ask__tune">
            <label htmlFor="search-max">{t('collections:ask.max')}</label>
            <input id="search-max" className="dk-input" type="number" min={1} max={100} value={maxResults} onChange={(e) => setMaxResults(parseInt(e.target.value, 10) || DEFAULT_PASSAGES)} />
          </div>
        </details>
        <div aria-live="polite">
          {answer && (
            answer.error ? (
              <Callout tone="error" role="alert">{t('library:search.failed', { message: answer.error })}</Callout>
            ) : answer.results.length === 0 ? (
              <p className="lib-note" data-testid="ask-none">{t('collections:ask.none')}</p>
            ) : (
              <div data-testid="ask-results">
                <p className="lib-muted lib-fine">{t('collections:ask.ranked', { count: answer.results.length })}</p>
                <PassageList results={answer.results} />
              </div>
            )
          )}
        </div>
      </section>

      <section className="lib-section" aria-labelledby="sources-h">
        <h3 className="lib-section__title" id="sources-h">{t('collections:sources.title')} {loaded && !sourcesError && <span className="lib-count">{sources.length}</span>}</h3>
        {!loaded ? (
          <SkeletonRows count={1} />
        ) : sourcesError ? (
          <LoadError title={t('collections:sources.loadFailed')} error={sourcesError} onRetry={fetchSources} />
        ) : sources.length === 0 ? (
          <p className="lib-muted" data-testid="sources-none">{t(ownerOnly ? 'collections:sources.none' : 'collections:sources.noneShared')}</p>
        ) : (
          <ul className="lib-ledger" data-testid="sources-list">
            {sources.map(s => (
              <li key={s.url} className="lib-line">
                <span className="lib-line__lead"><Icon name="globe" /></span>
                <span className="lib-line__main">
                  <span className="lib-line__name">{s.url}</span>
                  <span className="lib-line__meta">
                    {s.interval > 0 ? t('collections:sources.every', { count: s.interval }) : t('collections:sources.manual')}
                    {' · '}
                    {s.lastUpdate ? t('collections:sources.updated', { when: when(s.lastUpdate) }) : t('collections:sources.never')}
                  </span>
                </span>
                {ownerOnly && (
                  <span className="lib-line__acts">
                    <Button variant="ghost" size="sm" icon="trash" onClick={() => removeSource(s.url)} aria-label={t('collections:sources.remove', { url: s.url })} title={t('collections:sources.removeTitle')} />
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="lib-section" aria-labelledby="entries-h">
        <h3 className="lib-section__title" id="entries-h">{t('collections:entries.title')} {loaded && !entriesError && <span className="lib-count">{entries.length}</span>}</h3>
        {!loaded ? (
          <SkeletonRows count={2} />
        ) : entriesError ? (
          <LoadError title={t('collections:entries.loadFailed')} error={entriesError} onRetry={fetchEntries} />
        ) : entries.length === 0 && attempts.length === 0 ? (
          <p className="lib-muted" data-testid="entries-none">{t('collections:entries.none')}</p>
        ) : entries.length > 0 && (
          <>
            {entries.length > FILTER_FROM && (
              <div className="lib-filebar">
                <SearchField
                  value={fileQuery}
                  onChange={(v) => { setFileQuery(v); setFileLimit(FILES_PAGE) }}
                  label={t('collections:entries.filter')}
                  testId="entries-filter"
                />
                <span className="lib-filebar__count" role="status" aria-live="polite" data-testid="entries-count">
                  {matching.length === entries.length
                    ? t('collections:entries.countAll', { count: entries.length })
                    : t('collections:entries.countOf', { shown: matching.length, total: entries.length })}
                </span>
              </div>
            )}
            {matching.length === 0 ? (
              <p className="lib-muted" data-testid="entries-nomatch">{t('collections:entries.noMatch')}</p>
            ) : (
              <ul className="lib-ledger lib-ledger--dense" data-testid="entries-list">
                {matching.slice(0, fileLimit).map(({ entry, label }, i) => (
                  <li key={`${label}-${i}`} className="lib-line">
                    <span className="lib-line__lead"><Icon name="file-text" /></span>
                    <span className="lib-line__main"><span className="lib-line__name" title={label}>{label}</span></span>
                    <span className="lib-line__acts">
                      <Button variant="ghost" size="sm" icon="eye" onClick={() => openEntry(entry)} aria-label={t('collections:entries.view', { name: label })} title={t('collections:entries.viewTitle')} />
                      {canWrite && <Button variant="ghost" size="sm" icon="trash" onClick={() => deleteEntry(entry)} aria-label={t('collections:entries.delete', { name: label })} title={t('collections:entries.deleteTitle')} />}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {matching.length > fileLimit && (
              <Button variant="ghost" size="sm" className="lib-more-files" onClick={() => setFileLimit(n => n + FILES_PAGE)} data-testid="entries-more">
                {t('library:rail.more', { count: Math.min(FILES_PAGE, matching.length - fileLimit) })}
              </Button>
            )}
          </>
        )}
      </section>

      <details className="lib-disc" data-testid="collection-api">
        <summary><Icon name="chevron-right" />{t('collections:api.title')}</summary>
        <div className="lib-disc__body">
          <p>{t('collections:api.index')}</p>
          <ul className="lib-paths">
            {PATHS.map(([verb, path, key]) => (
              <li key={key}><span className="lib-verb">{verb}</span><code>{path.replace('{name}', encodeURIComponent(name))}</code></li>
            ))}
          </ul>
          <p>{t('collections:api.privacy')}</p>
        </div>
      </details>

      {view && (
        <div className="dk-veil" onClick={() => setView(null)}>
          <div className="dk-dialog dk-dialog--wide" role="dialog" aria-modal="true" aria-labelledby="entry-title" onClick={e => e.stopPropagation()} data-testid="entry-dialog">
            <div className="dk-dialog-head">
              <div>
                <h3 className="dk-dialog-title lib-mono" id="entry-title">{entryName(view.entry)}</h3>
                {view.data && <p className="dk-dialog-desc">{t('collections:entries.chunks', { count: view.data.chunk_count ?? 0 })}</p>}
              </div>
              <Button variant="ghost" size="sm" icon="close" aria-label={t('collections:entries.close')} onClick={() => setView(null)} />
            </div>
            <div className="dk-dialog-body">
              {view.loading ? <SkeletonRows count={3} />
                : view.data ? <pre className="lib-pre">{view.data.content || t('collections:entries.empty')}</pre>
                  : <p className="lib-muted">{t('collections:entries.noContent')}</p>}
            </div>
          </div>
        </div>
      )}
    </>
  )
}
