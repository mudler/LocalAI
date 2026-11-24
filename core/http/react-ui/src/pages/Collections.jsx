import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentCollectionsApi, sharesApi } from '../utils/api'
// eslint-disable-next-line no-unused-vars
import ShareDialog from '../components/share/ShareDialog'
// eslint-disable-next-line no-unused-vars
import { SharedByBadge } from '../components/share/ShareBadges'
import { kindShareClient } from '../components/share/shareClients'
import { useAuth } from '../context/AuthContext'
import { useUserMap } from '../hooks/useUserMap'
import { useLibraryFacts } from '../hooks/useLibraryFacts'
import { useWideLayout } from '../hooks/useWideLayout'
// eslint-disable-next-line no-unused-vars
import UserGroupSection from '../components/UserGroupSection'
// eslint-disable-next-line no-unused-vars
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SearchField, Segmented } from '../components/ui'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import SideSheet from '../components/SideSheet'
// eslint-disable-next-line no-unused-vars
import CollectionPane from '../components/library/CollectionPane'
// eslint-disable-next-line no-unused-vars
import { CountLine, DensityToggle, NoMatch, RailHeading, RailRow, RailSkeleton, ShowMore, SortMenu } from '../components/library/RailBits'
import { useRevealSelected, useRovingRows, useSlashToFocus } from '../hooks/useRail'
// eslint-disable-next-line no-unused-vars
import SimulateSheet from '../components/library/SimulateSheet'
import Icon from '../components/Icon'
import { usedByLine } from '../utils/libraryText'
import { collectionUsers, withCollection, withoutCollection } from '../utils/library'
import { matchesQuery, rowLimit, sortItems } from '../utils/libraryList'
import { useStoredChoice } from '../hooks/useStoredChoice'
import { chatSkillsApi } from '../utils/api'
import './library.css'

const DOCS = 'https://localai.io/features/collections/'
const STARTING_EVERY = 5000
const STARTING_TRIES = 12

const nameOf = c => (typeof c === 'string' ? c : c?.name)
// The kind of a collection in the sharing API.
const SHARE_KIND = 'collection'

// The orders the list offers and how many rows it draws at a time.
const SORTS = ['name', 'name-desc', 'used', 'unused']
const DENSITIES = ['comfortable', 'compact']
const ROWS_PAGE = 60

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
  const [filter, setFilterNow] = useState('')
  const [usage, setUsageNow] = useState('all')
  // The order and the row density are the person's habit, so they are kept.
  const [sort, setSort] = useStoredChoice('localai.library.collections.sort', 'name', SORTS)
  const [density, setDensity] = useStoredChoice('localai.library.density', 'comfortable', DENSITIES)
  const [shown, setShown] = useState(ROWS_PAGE)
  // A reset empties the open collection: a new key makes the pane read it again.
  const [paneNonce, setPaneNonce] = useState(0)
  const searchRef = useRef(null)
  const scrollRef = useRef(null)
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
  // How a search finds passages: 'semantic' or 'hybrid'. '' while unknown
  // (an older server does not say).
  const [searchMode, setSearchMode] = useState('')
  // The collections that other people shared with the user ({ name, title,
  // owner, role, everyone }), the shares of the user's own ones by name, and
  // whether this server shares at all (not in local-owner mode).
  const [shared, setShared] = useState([])
  const [shares, setShares] = useState({})
  const [sharing, setSharing] = useState(false)
  const [share, setShare] = useState(null)

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
      setSearchMode(data.search_mode === 'hybrid' || data.search_mode === 'semantic' ? data.search_mode : '')
      setShared(Array.isArray(data.shared) ? data.shared.filter(x => x && typeof x.name === 'string') : [])
      setShares(data.shares && typeof data.shares === 'object' ? data.shares : {})
      setSharing(data.sharing === true)
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

  // A new search or filter starts the list from its top again.
  const setFilter = (v) => { setFilterNow(v); setShown(ROWS_PAGE) }
  const setUsage = (v) => { setUsageNow(v); setShown(ROWS_PAGE) }
  const clearFilters = () => { setFilter(''); setUsage('all') }
  useSlashToFocus(searchRef)

  const visible = useMemo(() => {
    const rows = collections.filter(c => {
      if (!matchesQuery(filter, c)) return false
      if (known && usage === 'used' && usersOf.get(c).length === 0) return false
      if (known && usage === 'unused' && usersOf.get(c).length > 0) return false
      return true
    })
    return sortItems(rows, known ? sort : (sort === 'used' || sort === 'unused' ? 'name' : sort), c => ({ name: c, used: usersOf.get(c).length, group: '' }))
  }, [collections, filter, usage, sort, known, usersOf])
  // What other people shared, and what other users own, follow the same search.
  const sharedRows = useMemo(
    () => sortItems(shared.filter(x => matchesQuery(filter, x.name, x.title)), 'name', x => ({ name: x.title || x.name, used: 0, group: '' })),
    [shared, filter],
  )
  const othersGroups = useMemo(() => {
    if (!userGroups || !filter.trim()) return userGroups
    const out = {}
    for (const [uid, group] of Object.entries(userGroups)) {
      const keep = (group.collections || []).filter(c => matchesQuery(filter, nameOf(c)))
      if (keep.length > 0) out[uid] = { ...group, collections: keep }
    }
    return out
  }, [userGroups, filter])
  const counts = useMemo(() => {
    const used = collections.filter(c => usersOf.get(c).length > 0).length
    return { all: collections.length, used, unused: collections.length - used }
  }, [collections, usersOf])

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
  // A collection shared with the user, when the selected name is one: the
  // user's own collection of that name wins, as on the server.
  const sharedEntry = selected && !collections.includes(selected) ? (shared.find(x => x.name === selected) || null) : null
  const shareClient = useMemo(() => (share ? kindShareClient(sharesApi, SHARE_KIND, share.id) : null), [share])
  const openShare = async (name) => {
    try {
      const r = shares[name]?.resource_id ? shares[name] : await agentCollectionsApi.share(name)
      setShare({ id: r.resource_id, name })
    } catch (err) {
      addToast(t('collections:share.failed', { message: err.message }), 'error')
    }
  }
  const selectedOther = selectedUser && (userGroups?.[selectedUser]?.collections || []).map(nameOf).includes(selectedName) ? selectedName : ''
  const open = !!(selected || selectedOther)
  const empty = !loading && !loadError && collections.length === 0 && shared.length === 0 && !userGroups && !open
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

  // The rows drawn: a page of them, widened when the open one lies further down.
  const openIndex = visible.indexOf(selectedName)
  const drawn = visible.slice(0, rowLimit(shown, visible.length, !selectedUser ? openIndex : -1))
  const rowKeys = [...drawn, ...sharedRows.map(x => `shared:${x.name}`)]
  const roving = useRovingRows(rowKeys, selectedUser ? '' : (sharedEntry ? `shared:${selectedName}` : selectedName))
  useRevealSelected(scrollRef, `${selectedUser || ''}/${selectedName}`)
  const filtered = filter.trim() !== '' || usage !== 'all'
  const closeCreate = () => { setShowCreate(false); setNewName('') }
  // The quiet menu of a row: what a person does to a collection without opening it first.
  const menuFor = (c) => [
    { key: 'try', icon: 'flask', label: t('collections:menu.try'), onClick: () => setSim({ seed: { kind: 'collection', name: c } }) },
    ...(sharing ? [{ key: 'share', icon: 'share', label: t('collections:share.label'), onClick: () => openShare(c) }] : []),
    { divider: true },
    { key: 'reset', icon: 'eraser', danger: true, label: t('collections:actions.reset'), onClick: () => handleReset(c, undefined, () => setPaneNonce(n => n + 1)) },
  ]
  const sortOptions = SORTS.map(id => ({ id, label: t(`collections:sort.${id}`), disabled: !known && (id === 'used' || id === 'unused') })).filter(o => !o.disabled)
  const usageOptions = ['all', 'used', 'unused'].map(f => ({ id: f, label: t(`collections:filter.${f}`), count: known ? counts[f] : undefined, disabled: f !== 'all' && !known }))

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
              <Button variant="primary" icon="plus" aria-haspopup="dialog" onClick={() => setShowCreate(true)} data-testid="new-collection">
                {t('collections:actions.newCollection')}
              </Button>
            )}
          </>
        )}
      />

      {showCreate && (
        <SideSheet
          title={t('collections:create.title')}
          description={t('collections:create.hint')}
          onClose={closeCreate}
          closeLabel={t('collections:entries.close')}
          testId="create-form"
          labelId="create-collection-title"
          footer={(
            <>
              <Button variant="ghost" onClick={closeCreate}>{t('collections:actions.cancel')}</Button>
              <Button type="submit" form="create-collection-form" variant="primary" busy={creating} disabled={!newName.trim()}>
                {t('collections:actions.save')}
              </Button>
            </>
          )}
        >
          <form id="create-collection-form" onSubmit={handleCreate}>
            <div className="dk-field">
              <label className="dk-label" htmlFor="new-collection-name">{t('collections:newLabel')}</label>
              <input
                id="new-collection-name"
                className="dk-input"
                type="text"
                placeholder={t('collections:newPlaceholder')}
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </div>
          </form>
        </SideSheet>
      )}

      {!loading && !loadError && searchMode && (
        <p className="lib-mode" data-testid="search-mode">
          <strong>{t('collections:searchMode.label', { mode: t(`collections:searchMode.${searchMode}`) })}.</strong>{' '}
          {t(`collections:searchMode.${searchMode}Hint`)}
        </p>
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
        <RailSkeleton />
      ) : listError && !open ? (
        listError
      ) : empty ? (
        <EmptyState
          mascot="welcome"
          icon="database"
          title={t('collections:empty.title')}
          body={t('collections:empty.text')}
          steps={[t('collections:empty.stepCreate'), t('collections:empty.stepFill'), t('collections:empty.stepAsk')]}
          actions={(
            <>
              <Button variant="primary" icon="plus" aria-haspopup="dialog" onClick={() => setShowCreate(true)} data-testid="new-collection">
                {t('collections:actions.newCollection')}
              </Button>
              <Button variant="ghost" icon="book" href={DOCS} target="_blank" rel="noopener noreferrer">{t('collections:empty.learn')}</Button>
            </>
          )}
          testId="memory-empty"
        />
      ) : (
        <div className="dk-split lib-split" data-view={open ? 'pane' : 'list'}>
          <div className="dk-split-rail lib-rail" data-density={density}>
            {listError || (
              <>
                <div className="lib-bar">
                  <div className="lib-bar__row">
                    <SearchField
                      ref={searchRef}
                      value={filter}
                      onChange={setFilter}
                      label={t('collections:filter.label')}
                      placeholder={t('collections:filter.placeholder')}
                      testId="collections-filter"
                      onKeyDown={(e) => { if (e.key === 'Escape' && filter) { e.stopPropagation(); setFilter('') } }}
                    />
                    <DensityToggle value={density} onChange={setDensity} testId="collections-density" />
                  </div>
                  <div className="lib-bar__row">
                    <Segmented
                      options={usageOptions}
                      value={usage}
                      onChange={setUsage}
                      label={t('collections:filter.show')}
                      itemTestId="collections-filter-"
                    />
                    <SortMenu label={t('collections:sort.label')} value={sort} onChange={setSort} options={sortOptions} testId="collections-sort" />
                  </div>
                </div>
                {collections.length > 0 && (
                  <CountLine
                    text={filtered ? t('collections:list.countOf', { shown: visible.length, total: collections.length }) : t('collections:list.countAll', { count: collections.length })}
                    filtered={filtered}
                    onClear={clearFilters}
                    testId="collections-count"
                  />
                )}

                <div className="lib-scroll" ref={scrollRef} onKeyDown={roving.onKeyDown}>
                  {collections.length === 0 ? (
                    <p className="lib-rail__none">{t('collections:empty.noPersonal')}</p>
                  ) : visible.length === 0 ? (
                    sharedRows.length === 0 && !(othersGroups && Object.keys(othersGroups).length) && (
                      <NoMatch text={t('collections:filter.none')} onClear={clearFilters} testId="collections-none" />
                    )
                  ) : (
                    <ul className="lib-rows" role="list" aria-label={t('collections:list.label')}>
                      {sharing && sharedRows.length > 0 && <RailHeading count={visible.length}>{t('collections:sections.yourCollections')}</RailHeading>}
                      {drawn.map(c => (
                        <RailRow
                          key={c}
                          rowKey={c}
                          testId={`collection-row-${c}`}
                          selected={!selectedUser && selectedName === c}
                          stop={roving.stopKey === c}
                          onOpen={() => go(c)}
                          onFocus={() => roving.onRowFocus(c)}
                          icon="database"
                          title={c}
                          meta={usedByLine(t, usersOf.get(c), facts.status)}
                          metaTestId={`collection-used-${c}`}
                          extra={sharing && shares[c] ? <SharedByBadge entry={{ role: 'owner', sharedWith: shares[c].shared_with }} hideRole testId={`collection-shared-with-${c}`} /> : null}
                          menu={menuFor(c)}
                          menuLabel={t('collections:list.menu', { name: c })}
                        />
                      ))}
                      <ShowMore remaining={visible.length - drawn.length} step={ROWS_PAGE} onMore={() => setShown(n => n + ROWS_PAGE)} testId="collections-more" />
                    </ul>
                  )}

                  {sharing && sharedRows.length > 0 && (
                    <ul className="lib-rows lib-shared" role="list" aria-label={t('collections:sections.sharedWithYou')} data-testid="collections-shared">
                      <RailHeading count={sharedRows.length}>{t('collections:sections.sharedWithYou')}</RailHeading>
                      {sharedRows.map(x => (
                        <RailRow
                          key={x.name}
                          rowKey={`shared:${x.name}`}
                          testId={`collection-shared-row-${x.name}`}
                          selected={!selectedUser && selectedName === x.name}
                          stop={roving.stopKey === `shared:${x.name}`}
                          onOpen={() => go(x.name)}
                          onFocus={() => roving.onRowFocus(`shared:${x.name}`)}
                          icon="users"
                          title={x.title || x.name}
                          extra={<SharedByBadge entry={{ role: x.role, owner: x.owner, everyone: x.everyone }} roleLabel={t(`collections:share.roles.${x.role === 'editor' ? 'editor' : 'viewer'}`)} />}
                        />
                      ))}
                    </ul>
                  )}

                  {othersGroups && (
                    <UserGroupSection
                      title={t('collections:sections.otherUsersCollections')}
                      userGroups={othersGroups}
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
                              aria-current={selectedUser === userId && selectedName === c ? 'true' : undefined}
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
                </div>
              </>
            )}
          </div>

          <section className="dk-card lib-pane" aria-live="polite" data-testid="collection-pane">
            {open ? (
              <CollectionPane
                key={`${selectedUser || ''}/${selectedName}/${paneNonce}`}
                t={t}
                name={selectedName}
                userId={selectedUser}
                users={selectedUser || sharedEntry ? [] : collectionUsers(facts.agents, selectedName)}
                status={selectedUser || sharedEntry ? 'other' : facts.status}
                shared={sharedEntry}
                onShare={sharing && !selectedUser && !sharedEntry && collections.includes(selectedName) ? () => openShare(selectedName) : null}
                shareLine={sharing && !sharedEntry ? shares[selectedName] : null}
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

      {share && shareClient && (
        <ShareDialog
          client={shareClient}
          resource={{ id: share.id, name: share.name }}
          sharing={sharing}
          roles={[
            { id: 'viewer', label: t('collections:share.roles.viewer'), help: t('collections:share.roles.viewerHelp') },
            { id: 'editor', label: t('collections:share.roles.editor'), help: t('collections:share.roles.editorHelp') },
          ]}
          allowEveryone
          intro={t('collections:share.intro')}
          onChanged={fetchCollections}
          onLost={() => { setShare(null); fetchCollections() }}
          close={() => setShare(null)}
          testId="collection-share-dialog"
        />
      )}

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
