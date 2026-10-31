import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentCollectionsApi } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import { useLibraryFacts } from '../hooks/useLibraryFacts'
import { useWideLayout } from '../hooks/useWideLayout'
// eslint-disable-next-line no-unused-vars
import UserGroupSection from '../components/UserGroupSection'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SkeletonRows, StatusChip } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import { AddToMenu, PassageList, UsedByStrip } from '../components/library/LibraryBits'
// eslint-disable-next-line no-unused-vars
import SimulateSheet from '../components/library/SimulateSheet'
import { usedByLine } from '../utils/libraryText'
import { collectionUsers, entryName, normaliseSource, withCollection, withoutCollection } from '../utils/library'
import { chatSkillsApi } from '../utils/api'
import './library.css'

const DEFAULT_PASSAGES = 10
const DOCS = 'https://localai.io/features/collections/'
const STARTING_EVERY = 5000
const STARTING_TRIES = 12

const nameOf = c => (typeof c === 'string' ? c : c?.name)

function when(ts) {
  try { return new Date(ts).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) } catch { return '' }
}

export default function Collections() {
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const { name: routeName } = useParams()
  const [params] = useSearchParams()
  const { t } = useTranslation(['collections', 'library'])
  const { isAdmin, authEnabled, user } = useAuth()
  const userMap = useUserMap()
  const facts = useLibraryFacts()
  const wide = useWideLayout()

  const [collections, setCollections] = useState([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(null)
  const [filter, setFilter] = useState('')
  const [usage, setUsage] = useState('all')
  const [showCreate, setShowCreate] = useState(false)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)
  const [userGroups, setUserGroups] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [sim, setSim] = useState(null)
  const [skills, setSkills] = useState([])
  const [busy, setBusy] = useState(false)
  // The model that indexes and searches the collections, and whether this
  // server has it. Without it every upload and search fails.
  const [embedding, setEmbedding] = useState({ model: '', installed: true })

  const selectedUser = params.get('user_id') || undefined

  const go = useCallback((name, userId) => {
    if (!name) { navigate('/app/collections'); return }
    navigate(`/app/collections/${encodeURIComponent(name)}${userId ? `?user_id=${encodeURIComponent(userId)}` : ''}`)
  }, [navigate])

  const fetchCollections = useCallback(async () => {
    try {
      const data = await agentCollectionsApi.list(isAdmin && authEnabled)
      setCollections((Array.isArray(data.collections) ? data.collections : []).map(nameOf).filter(Boolean))
      setUserGroups(data.user_groups || null)
      setEmbedding({ model: data.embedding_model || '', installed: data.embedding_model_installed !== false })
      setLoadError(null)
    } catch (err) {
      // A failed read shows an error in place of the list, never the empty
      // library, and never only a toast.
      setLoadError(err)
    } finally {
      setLoading(false)
    }
  }, [isAdmin, authEnabled])

  useEffect(() => { fetchCollections() }, [fetchCollections])

  // While the memory service starts the server answers 503. That is not a
  // failure: the page says so and asks again by itself, for about a minute.
  const startTries = useRef(0)
  const starting = loadError?.status === 503
  useEffect(() => {
    if (!starting) { startTries.current = 0; return undefined }
    if (startTries.current >= STARTING_TRIES) return undefined
    const id = setTimeout(() => { startTries.current += 1; fetchCollections() }, STARTING_EVERY)
    return () => clearTimeout(id)
  }, [starting, loadError, fetchCollections])

  // Skills only feed the Simulate sheet.
  useEffect(() => {
    chatSkillsApi.list()
      .then(data => setSkills(Array.isArray(data?.skills) ? data.skills.filter(sk => !sk.shadowed && !sk.invalid) : []))
      .catch(() => setSkills([]))
  }, [])

  const known = facts.status === 'ready'
  const usersOf = useMemo(() => {
    const map = new Map()
    for (const c of collections) map.set(c, collectionUsers(facts.agents, c))
    return map
  }, [collections, facts.agents])

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return collections.filter(c => {
      if (q && !c.toLowerCase().includes(q)) return false
      if (known && usage === 'used' && usersOf.get(c).length === 0) return false
      if (known && usage === 'unused' && usersOf.get(c).length > 0) return false
      return true
    })
  }, [collections, filter, usage, known, usersOf])

  const handleCreate = async (e) => {
    e?.preventDefault?.()
    const name = newName.trim()
    if (!name) return
    setCreating(true)
    try {
      await agentCollectionsApi.create(name)
      addToast(t('collections:toasts.created', { name }), 'success')
      setNewName('')
      setShowCreate(false)
      await fetchCollections()
      go(name)
    } catch (err) {
      addToast(t('collections:toasts.createFailed', { message: err.message }), 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleReset = (name, userId, after) => {
    setConfirmDialog({
      title: t('collections:resetDialog.title'),
      message: t('collections:resetDialog.message', { name }),
      confirmLabel: t('collections:resetDialog.confirm'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await agentCollectionsApi.reset(name, userId)
          addToast(t('collections:toasts.reset', { name }), 'success')
          after?.()
          fetchCollections()
        } catch (err) {
          addToast(t('collections:toasts.resetFailed', { message: err.message }), 'error')
        }
      },
    })
  }

  // ---- Attach and detach -------------------------------------------------
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

  const addToAgent = async (collection, agent) => {
    const config = facts.agents.find(a => a.name === agent)?.config
    if (!config) { addToast(t('library:toasts.updateFailed', { agent, message: t('library:usedBy.unread') }), 'error'); return }
    const next = withCollection(config, collection)
    if (!next) return
    if (await saveFor(agent, next)) addToast(t('library:toasts.collectionAdded', { name: collection, agent }), 'success')
  }

  const removeFromAgent = async (collection, agent) => {
    const config = facts.agents.find(a => a.name === agent)?.config
    const next = config && withoutCollection(config, collection)
    if (!next) return
    if (!(await saveFor(agent, next))) return
    addToast(t('library:toasts.collectionRemoved', { name: collection, agent }), 'success', undefined, {
      testId: 'library-undo-toast',
      action: {
        label: t('library:toasts.undo'),
        onClick: async () => { if (await saveFor(agent, config)) addToast(t('library:toasts.undone'), 'success') },
      },
    })
  }

  // A link can name a collection the list has not shown yet; the pane reads it
  // from the server and says so if it is not there.
  // Side by side, the first collection is open until another is picked. On a
  // phone nothing opens until the person taps one.
  const selectedName = routeName || (wide && !selectedUser ? (visible[0] || '') : '')
  const selected = !selectedUser && selectedName ? selectedName : ''
  const selectedOther = selectedUser && (userGroups?.[selectedUser]?.collections || []).map(nameOf).includes(selectedName) ? selectedName : ''
  const open = !!(selected || selectedOther)
  const empty = !loading && !loadError && collections.length === 0 && !userGroups && !open
  // A list that could not be read replaces the list only; a collection named
  // in the address still opens in its pane.
  const listError = !loadError ? null : starting ? (
    <Callout
      title={t('collections:starting.title')}
      role="status"
      testId="memory-starting"
      actions={<Button size="sm" icon="refresh" onClick={fetchCollections}>{t('common:ui.tryAgain')}</Button>}
    >
      {t('collections:starting.text')}
    </Callout>
  ) : (
    <LoadError title={t('collections:loadFailed')} error={loadError} onRetry={fetchCollections} />
  )

  return (
    <Page width="full" className="lib-page" data-testid="memory-page">
      <PageHeader
        title={t('collections:title')}
        supporting={t('collections:subtitle')}
        actions={!loadError && (
          <>
            <Button icon="flask" onClick={() => setSim({ seed: null })} data-testid="open-simulate">
              {t('library:simulate.button')}
            </Button>
            {!empty && (
              <Button variant="primary" icon="plus" aria-expanded={showCreate} onClick={() => setShowCreate(v => !v)} data-testid="new-collection">
                {t('collections:actions.newCollection')}
              </Button>
            )}
          </>
        )}
      />

      {showCreate && (
        <form className="lib-create lib-create--page" onSubmit={handleCreate} data-testid="create-form">
          <input
            className="dk-input"
            type="text"
            aria-label={t('collections:newLabel')}
            placeholder={t('collections:newPlaceholder')}
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            autoFocus
          />
          <Button type="submit" variant="primary" icon="plus" busy={creating} disabled={!newName.trim()}>
            {t('collections:actions.create')}
          </Button>
          <Button variant="ghost" onClick={() => { setShowCreate(false); setNewName('') }}>{t('collections:actions.cancel')}</Button>
        </form>
      )}

      {!loading && !loadError && !embedding.installed && (
        <Callout
          tone="warn"
          title={t('collections:embedding.missingTitle')}
          testId="memory-no-embedding"
          actions={isAdmin && embedding.model ? (
            <Button size="sm" variant="primary" icon="download" onClick={() => navigate(`/app/models?q=${encodeURIComponent(embedding.model)}`)} data-testid="install-embedding">
              {t('collections:embedding.install')}
            </Button>
          ) : null}
        >
          {embedding.model ? t('collections:embedding.missingText', { model: embedding.model }) : t('collections:embedding.noneSet')}
          {!isAdmin && ` ${t('collections:embedding.askAdmin')}`}
        </Callout>
      )}

      {loading ? (
        <SkeletonRows count={3} />
      ) : listError && !open ? (
        listError
      ) : empty ? (
        <EmptyState
          icon="database"
          title={t('collections:empty.title')}
          body={t('collections:empty.text')}
          steps={[t('collections:empty.stepCreate'), t('collections:empty.stepFill'), t('collections:empty.stepAsk')]}
          actions={(
            <>
              <Button variant="primary" icon="plus" aria-expanded={showCreate} onClick={() => setShowCreate(true)} data-testid="new-collection">
                {t('collections:actions.newCollection')}
              </Button>
              <Button variant="ghost" icon="book" href={DOCS} target="_blank" rel="noopener noreferrer">{t('collections:empty.learn')}</Button>
            </>
          )}
          testId="memory-empty"
        />
      ) : (
        <div className="dk-split lib-split" data-view={open ? 'pane' : 'list'}>
          <div className="dk-split-rail lib-rail">
            {listError || (
              <>
                <span className="dk-input-icon lib-find">
                  <Icon name="search" className="dk-icon" />
                  <input
                    type="search"
                    className="dk-input"
                    placeholder={t('collections:filter.placeholder')}
                    aria-label={t('collections:filter.label')}
                    value={filter}
                    onChange={(e) => setFilter(e.target.value)}
                    data-testid="collections-filter"
                  />
                </span>
                <div className="lib-filters" role="group" aria-label={t('collections:filter.show')}>
                  {['all', 'used', 'unused'].map(f => (
                    <button
                      key={f}
                      type="button"
                      className="dk-chip dk-chip--sm"
                      aria-pressed={usage === f}
                      disabled={f !== 'all' && !known}
                      onClick={() => setUsage(f)}
                      data-testid={`collections-filter-${f}`}
                    >
                      {t(`collections:filter.${f}`)}
                    </button>
                  ))}
                </div>

                {collections.length === 0 ? (
                  <p className="lib-rail__none">{t('collections:empty.noPersonal')}</p>
                ) : visible.length === 0 ? (
                  <p className="lib-rail__none" data-testid="collections-none">{t('collections:filter.none')}</p>
                ) : (
                  <div className="dk-list lib-rows" role="list" aria-label={t('collections:list.label')}>
                    {visible.map(c => (
                      <button
                        key={c}
                        type="button"
                        role="listitem"
                        className="dk-row"
                        aria-selected={!selectedUser && selectedName === c}
                        onClick={() => go(c)}
                        data-testid={`collection-row-${c}`}
                      >
                        <span className="dk-row-lead"><Icon name="database" /></span>
                        <span className="dk-row-main">
                          <span className="dk-row-title">{c}</span>
                          <span className="dk-row-meta" data-testid={`collection-used-${c}`}>{usedByLine(t, usersOf.get(c), facts.status)}</span>
                        </span>
                      </button>
                    ))}
                  </div>
                )}

                {userGroups && (
                  <UserGroupSection
                    title={t('collections:sections.otherUsersCollections')}
                    userGroups={userGroups}
                    userMap={userMap}
                    currentUserId={user?.id}
                    itemKey="collections"
                    renderGroup={(items, userId) => (
                      <div className="dk-list lib-others">
                        {(items || []).map(nameOf).map(c => (
                          <button
                            key={c}
                            type="button"
                            className="dk-row"
                            aria-selected={selectedUser === userId && selectedName === c}
                            onClick={() => go(c, userId)}
                          >
                            <span className="dk-row-lead"><Icon name="database" /></span>
                            <span className="dk-row-main"><span className="dk-row-title">{c}</span></span>
                          </button>
                        ))}
                      </div>
                    )}
                  />
                )}
              </>
            )}
          </div>

          <section className="dk-card lib-pane" aria-live="polite" data-testid="collection-pane">
            {open ? (
              <CollectionPane
                key={`${selectedUser || ''}/${selectedName}`}
                t={t}
                name={selectedName}
                userId={selectedUser}
                users={selectedUser ? [] : collectionUsers(facts.agents, selectedName)}
                status={selectedUser ? 'other' : facts.status}
                agents={facts.agents.filter(a => a.config)}
                busy={busy}
                addToast={addToast}
                onBack={() => go('')}
                onReset={() => handleReset(selectedName, selectedUser)}
                onAdd={(agent) => addToAgent(selectedName, agent)}
                onRemove={(agent) => removeFromAgent(selectedName, agent)}
                onTry={() => setSim({ seed: { kind: 'collection', name: selectedName } })}
                setConfirmDialog={setConfirmDialog}
              />
            ) : (
              <div className="lib-pane__empty" data-testid="collection-pane-empty">
                <p>{t('collections:pane.pick')}</p>
              </div>
            )}
          </section>
        </div>
      )}

      <SimulateSheet
        open={!!sim}
        onClose={() => setSim(null)}
        agents={facts.agents.filter(a => a.config)}
        agentsStatus={facts.status}
        skills={skills}
        collections={collections}
        seed={sim?.seed || null}
      />

      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />
    </Page>
  )
}

const PATHS = [
  ['POST', '/api/collections/{name}/upload', 'upload'],
  ['GET', '/api/collections/{name}/entries', 'entries'],
  ['POST', '/api/collections/{name}/search', 'search'],
  ['GET', '/api/collections/{name}/sources', 'sources'],
  ['POST', '/api/collections/{name}/reset', 'reset'],
]

// eslint-disable-next-line no-unused-vars
function CollectionPane({ t, name, userId, users, status, agents, busy, addToast, onBack, onReset, onAdd, onRemove, onTry, setConfirmDialog }) {
  const own = !userId
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
          <h2 data-testid="collection-title">{name}</h2>
          <p className="lib-pane__sub" data-testid="collection-counts">{sub}</p>
        </div>
        <div className="lib-pane__acts">
          {failed > 0 && <StatusChip tone="error" testId="collection-failed-badge">{t('collections:pane.failed', { count: failed })}</StatusChip>}
          <Button variant="primary" size="sm" icon="plus" aria-expanded={addOpen} onClick={() => setAddOpen(v => !v)} data-testid="add-source-toggle">
            {t('collections:pane.addSource')}
          </Button>
          <Button variant="danger-ghost" size="sm" icon="eraser" onClick={onReset} title={t('collections:actions.resetCollection')}>
            {t('collections:actions.reset')}
          </Button>
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

      {addOpen && (
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
          <form className="lib-adding__row" onSubmit={handleAddSource}>
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
          </form>
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
          <p className="lib-muted" data-testid="sources-none">{t('collections:sources.none')}</p>
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
                <span className="lib-line__acts">
                  <Button variant="ghost" size="sm" icon="trash" onClick={() => removeSource(s.url)} aria-label={t('collections:sources.remove', { url: s.url })} title={t('collections:sources.removeTitle')} />
                </span>
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
          <ul className="lib-ledger" data-testid="entries-list">
            {entries.map((entry, i) => {
              const label = entryName(entry)
              return (
                <li key={`${label}-${i}`} className="lib-line">
                  <span className="lib-line__lead"><Icon name="file-text" /></span>
                  <span className="lib-line__main"><span className="lib-line__name" title={label}>{label}</span></span>
                  <span className="lib-line__acts">
                    <Button variant="ghost" size="sm" icon="eye" onClick={() => openEntry(entry)} aria-label={t('collections:entries.view', { name: label })} title={t('collections:entries.viewTitle')} />
                    <Button variant="ghost" size="sm" icon="trash" onClick={() => deleteEntry(entry)} aria-label={t('collections:entries.delete', { name: label })} title={t('collections:entries.deleteTitle')} />
                  </span>
                </li>
              )
            })}
          </ul>
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
