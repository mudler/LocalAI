/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
import ConfirmDialog from '../components/ConfirmDialog'
import Icon from '../components/Icon'
import { Button, Callout, EmptyState, LoadError, PageHeader, PaneLayout, SkeletonRows } from '../components/ui'
import ConnectorRow from '../components/mcp/ConnectorRow'
import ConnectorDetails from '../components/mcp/ConnectorDetails'
import ConnectorSheet from '../components/mcp/ConnectorSheet'
import { useAuth } from '../context/AuthContext'
import { chatMCPConnectorsApi } from '../utils/api'
import {
  STATE_FILTERS, browserImportBody, checkRecord, connectorState, currentCheck, dropCheck, emptyMCP, filterConnectors,
  forgetBrowserServers, formOf, mcpPayload, mcpTarget, readBrowserServers, readChecks, writeChecks,
} from '../utils/chatMCP'
import '../components/addons/addons.css'
import '../components/mcp/mcp.css'

const storage = () => { try { return window.localStorage } catch { return null } }
const DOCS = 'https://localai.io/features/chats-connectors/#mcp-servers'
// The search and the filter show from this many connectors: below it the list
// fits on one screen and the toolbar is noise.
const TOOLBAR_FROM = 4

function ImportBanner({ servers, busy, onImport, t }) {
  return (
    <Callout
      className="mcp-import"
      title={t('mcpPage.import.title')}
      testId="mcp-import"
      role="note"
      icon="import"
      actions={(
        <Button variant="primary" size="sm" icon="import" busy={busy} onClick={onImport}>
          {busy ? t('mcpPage.import.busy') : servers.length === 1 ? t('mcpPage.import.buttonOne') : t('mcpPage.import.button', { count: servers.length })}
        </Button>
      )}
    >
      {t('mcpPage.import.text')}
      <ul className="mcp-import__list">
        {servers.map((s) => (
          <li key={s.id || s.url}>
            <span className="mcp-import__name">{s.name || mcpTarget({ transport: 'http', url: s.url })}</span>
            <span className="mcp-mono">{mcpTarget({ transport: 'http', url: s.url })}</span>
            {s.headerCount > 0 && <span>{t('mcpPage.import.headersCount', { count: s.headerCount })}</span>}
          </li>
        ))}
      </ul>
    </Callout>
  )
}

// The user's MCP connectors: MCP servers that chats and saved agents can use.
// The list follows the Add-ons page (rows, a state chip each, search and a
// state filter, details beside the list); add and edit are a side sheet that
// saves, then tests, then turns the connector on.
export default function ChatMCPConnectors() {
  const { t } = useTranslation('chats')
  const { isAdmin, hasFeature } = useAuth()
  const { addToast } = useOutletContext() || {}
  const [params, setParams] = useSearchParams()
  const [items, setItems] = useState(null)
  const [loadError, setLoadError] = useState(null)
  const [error, setError] = useState('')
  const [forbidden, setForbidden] = useState('')
  const [note, setNote] = useState('')
  const [checks, setChecks] = useState(() => { const s = storage(); return s ? readChecks(s) : {} })
  const [testing, setTesting] = useState({})
  const [busy, setBusy] = useState({})
  const [sheet, setSheet] = useState(null)
  const [toRemove, setToRemove] = useState(null)
  const [removing, setRemoving] = useState(false)
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [testingAll, setTestingAll] = useState(false)
  const [browser, setBrowser] = useState(() => { const s = storage(); return s ? readBrowserServers(s) : [] })
  const [importing, setImporting] = useState(false)
  const alive = useRef(true)
  useEffect(() => () => { alive.current = false }, [])

  const fail = useCallback((e) => {
    if (e?.status === 403) { setForbidden(e.message || t('mcpPage.forbidden')); return }
    setError(e?.message || t('errors.send'))
  }, [t])

  // A failed read replaces the list with an error; it never looks like an
  // empty list. A refresh after an action keeps what is on screen.
  const load = useCallback(async () => {
    try {
      const res = await chatMCPConnectorsApi.list()
      setItems(res.connectors || [])
      setLoadError(null)
      setForbidden('')
    } catch (e) {
      if (e?.status === 403) { fail(e); return }
      setLoadError(e)
    }
  }, [fail])

  useEffect(() => { load() }, [load])

  // The last test of each connector stays in this browser, so a returning
  // person sees the last known state with its time.
  const remember = useCallback((c, record) => {
    setChecks(prev => {
      const next = { ...prev, [c.id]: record }
      const s = storage()
      if (s) writeChecks(s, next)
      return next
    })
  }, [])

  const test = useCallback(async (c) => {
    setTesting(x => ({ ...x, [c.id]: true }))
    let record
    try {
      record = checkRecord(c, await chatMCPConnectorsApi.test(c.id))
    } catch (e) {
      if (e?.status === 403) { fail(e); setTesting(x => ({ ...x, [c.id]: false })); return null }
      record = checkRecord(c, { ok: false, error: e?.message || t('errors.send') })
    }
    remember(c, record)
    setTesting(x => ({ ...x, [c.id]: false }))
    return record
  }, [fail, remember, t])

  const testAll = async () => {
    setTestingAll(true)
    for (const c of (items || []).filter(x => x.enabled)) {
      if (!alive.current) return
      await test(c)
    }
    setTestingAll(false)
    addToast?.(t('mcpPage.toolbar.testedAll'), 'info')
  }

  const toggle = async (c, on) => {
    setError('')
    setBusy(x => ({ ...x, [c.id]: true }))
    setItems(list => list.map(x => (x.id === c.id ? { ...x, enabled: on } : x)))
    try {
      const res = on ? await chatMCPConnectorsApi.enable(c.id) : await chatMCPConnectorsApi.disable(c.id)
      if (res && res.id) setItems(list => list.map(x => (x.id === c.id ? { ...x, ...res } : x)))
      addToast?.(on ? t('mcpPage.toasts.on', { name: c.name }) : t('mcpPage.toasts.off', { name: c.name }), 'success')
    } catch (e) {
      setItems(list => list.map(x => (x.id === c.id ? { ...x, enabled: !on } : x)))
      fail(e)
    } finally {
      setBusy(x => ({ ...x, [c.id]: false }))
    }
  }

  const remove = async () => {
    const c = toRemove
    setRemoving(true)
    setError('')
    try {
      await chatMCPConnectorsApi.remove(c.id)
      setToRemove(null)
      if (params.get('detail') === c.id) closeDetail()
      addToast?.(t('mcpPage.toasts.removed', { name: c.name }), 'success')
      await load()
    } catch (e) {
      setToRemove(null)
      fail(e)
    } finally {
      setRemoving(false)
    }
  }

  // The sheet saves and hands back the stored connector; the list follows.
  const save = async (form) => {
    const body = mcpPayload(form)
    const c = form.id ? await chatMCPConnectorsApi.update(form.id, body) : await chatMCPConnectorsApi.create(body)
    setChecks(prev => {
      const next = dropCheck(prev, c.id)
      const s = storage()
      if (s) writeChecks(s, next)
      return next
    })
    setItems(list => {
      const rest = (list || []).filter(x => x.id !== c.id)
      return [...rest, c].sort((a, b) => a.name.localeCompare(b.name))
    })
    // No toast: the sheet stays open and says it saved.
    return c
  }

  const turnOn = async (c) => {
    const res = await chatMCPConnectorsApi.enable(c.id)
    const next = res && res.id ? res : { ...c, enabled: true }
    setItems(list => list.map(x => (x.id === c.id ? next : x)))
    addToast?.(t('mcpPage.toasts.on', { name: c.name }), 'success')
    return next
  }

  const openSheet = (form, focus) => { setError(''); setNote(''); setSheet({ form, focus, key: Date.now() }) }
  const openAdd = () => openSheet(emptyMCP())
  const openEdit = (c) => openSheet(formOf(c))
  const openReauth = (c) => openSheet(formOf(c), 'token')
  const closeSheet = () => { setSheet(null); load() }

  const detailId = params.get('detail') || ''
  const openDetail = (id) => setParams(prev => { const out = new URLSearchParams(prev); out.set('detail', id); return out })
  function closeDetail() {
    const id = detailId
    setParams(prev => { const out = new URLSearchParams(prev); out.delete('detail'); return out }, { replace: true })
    requestAnimationFrame(() => document.querySelector(`[data-testid="mcp-row"][data-id="${CSS.escape(id)}"] .ad-tile__open`)?.focus())
  }

  const importAll = async () => {
    const s = storage()
    if (!s) return
    setImporting(true)
    setError('')
    setNote('')
    const taken = (items || []).map((c) => c.name)
    const done = []
    const problems = []
    for (const server of browser) {
      const body = browserImportBody(server, taken)
      try {
        await chatMCPConnectorsApi.create(body)
        taken.push(body.name)
        done.push(server.id)
      } catch (e) {
        problems.push(t('mcpPage.import.failed', { name: server.name || mcpTarget({ transport: 'http', url: server.url }), error: e?.message || '' }))
      }
    }
    // A token leaves the browser only for a server the API accepted.
    forgetBrowserServers(s, done)
    setBrowser(readBrowserServers(s))
    setNote([done.length ? t('mcpPage.import.done', { count: done.length }) : '', ...problems, problems.length ? t('mcpPage.import.left') : ''].filter(Boolean).join(' '))
    setImporting(false)
    await load()
  }

  const view = useMemo(() => filterConnectors(items || [], { query, filter, checks }), [items, query, filter, checks])
  const loading = items === null && !loadError && !forbidden
  const empty = items !== null && items.length === 0 && !loadError
  const detail = (items || []).find(c => c.id === detailId) || null
  const header = (
    <PageHeader
      title={t('mcpPage.title')}
      supporting={(
        <>
          {t('mcpPage.supporting')}
          {/* Telegram and Slack bots were next to MCP servers once: say where they went. */}
          {hasFeature('connectors') && <> <span className="ad-family"><Trans t={t} i18nKey="mcpPage.family" components={{ go: <Link to="/app/chats/connectors" /> }} /></span></>}
        </>
      )}
      actions={!empty && !forbidden && !loadError ? (
        <Button variant="primary" icon="plus" disabled={loading} onClick={openAdd} data-testid="mcp-add">{t('mcpPage.add')}</Button>
      ) : null}
    />
  )

  if (forbidden) {
    return (
      <section className="mcp-body" data-testid="mcp-connectors">
        {header}
        <EmptyState variant="unavailable" title={forbidden} testId="mcp-forbidden" />
      </section>
    )
  }

  const enabledCount = (items || []).filter(x => x.enabled).length
  return (
    <section className="mcp-body" data-testid="mcp-connectors">
      {header}

      {error && (
        <Callout tone="error" role="alert" testId="mcp-error"
          actions={<Button variant="ghost" size="sm" onClick={() => setError('')}>{t('errors.dismiss')}</Button>}>
          {error}
        </Callout>
      )}
      {note && <Callout tone="ok" role="status">{note}</Callout>}
      {browser.length > 0 && <ImportBanner servers={browser} busy={importing} onImport={importAll} t={t} />}

      {loading && <SkeletonRows count={3} label={t('mcpPage.loading')} />}
      {loadError && <LoadError title={t('mcpPage.loadFailed')} error={loadError} onRetry={load} testId="mcp-load-error" />}

      {empty && (
        <EmptyState
          icon="plug"
          title={t('mcpPage.empty.title')}
          body={t('mcpPage.empty.body')}
          facts={[{ text: t('mcpPage.empty.fact1') }, { text: t('mcpPage.empty.fact2') }, { text: t('mcpPage.empty.fact3') }]}
          actions={(
            <>
              <Button variant="primary" icon="plus" onClick={openAdd} data-testid="mcp-add">{t('mcpPage.add')}</Button>
              <Button variant="ghost" icon="book" href={DOCS} target="_blank" rel="noopener noreferrer">{t('mcpPage.learn')}</Button>
            </>
          )}
          testId="mcp-empty"
        />
      )}

      {items && items.length > 0 && !loadError && (
        <PaneLayout className="mcp-layout">
          <div className="mcp-list-col">
            {items.length >= TOOLBAR_FROM && (
              <div className="ad-toolbar mcp-toolbar" data-testid="mcp-toolbar">
                <span className="ad-search">
                  <Icon name="search" aria-hidden="true" />
                  <input type="search" className="dk-input ad-search__input" data-testid="mcp-search" aria-label={t('mcpPage.toolbar.search')}
                    placeholder={t('mcpPage.toolbar.search')} value={query} onChange={(e) => setQuery(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Escape' && query) { e.preventDefault(); setQuery('') } }} />
                </span>
                <div className="ad-seg" role="group" aria-label={t('mcpPage.filter.label')}>
                  {STATE_FILTERS.map(id => (
                    <button key={id} type="button" className="ad-seg__item" aria-pressed={filter === id} onClick={() => setFilter(id)} data-testid={`mcp-filter-${id}`}>
                      {t(`mcpPage.filter.${id}`)}
                      <span className="mcp-seg__count">{view.counts[id]}</span>
                    </button>
                  ))}
                </div>
                {enabledCount > 0 && (
                  <Button size="sm" variant="ghost" icon="refresh" busy={testingAll} disabled={testingAll} onClick={testAll} data-testid="mcp-test-all">
                    {testingAll ? t('mcpPage.toolbar.testingAll') : t('mcpPage.toolbar.testAll')}
                  </Button>
                )}
              </div>
            )}

            {view.rows.length === 0 ? (
              query.trim() ? (
                <EmptyState variant="no-results" title={t('mcpPage.noMatch.title', { query: query.trim() })} body={t('mcpPage.noMatch.body')} testId="mcp-no-match"
                  actions={<Button size="sm" variant="ghost" onClick={() => setQuery('')}>{t('mcpPage.noMatch.clear')}</Button>} />
              ) : (
                <EmptyState variant="no-results" title={t(`mcpPage.filter.none.${filter}`)} testId="mcp-no-match"
                  actions={<Button size="sm" variant="ghost" onClick={() => setFilter('all')}>{t('mcpPage.filter.showAll')}</Button>} />
              )
            ) : (
              <ul className="ad-tiles mcp-list" aria-label={t('mcpPage.title')}>
                {view.rows.map(c => (
                  <ConnectorRow
                    key={c.id}
                    c={c}
                    state={view.states.get(c.id)}
                    check={currentCheck(c, checks)}
                    selected={c.id === detailId}
                    testing={Boolean(testing[c.id])}
                    busy={Boolean(busy[c.id])}
                    onOpen={openDetail}
                    onToggle={toggle}
                    onTest={test}
                    onEdit={openEdit}
                    onReauth={openReauth}
                    onRemove={setToRemove}
                  />
                ))}
              </ul>
            )}
          </div>
          {detail && (
            <ConnectorDetails
              key={detail.id}
              c={detail}
              state={connectorState(detail, currentCheck(detail, checks))}
              check={currentCheck(detail, checks)}
              testing={Boolean(testing[detail.id])}
              busy={Boolean(busy[detail.id])}
              onClose={closeDetail}
              onToggle={toggle}
              onTest={test}
              onEdit={openEdit}
              onReauth={openReauth}
              onRemove={setToRemove}
            />
          )}
        </PaneLayout>
      )}

      {sheet && (
        <ConnectorSheet
          key={sheet.key}
          initial={sheet.form}
          focus={sheet.focus}
          isAdmin={isAdmin}
          items={items || []}
          onSave={save}
          onTest={async (c) => (await test(c)) || checkRecord(c, { ok: false, error: t('errors.send') })}
          onTurnOn={turnOn}
          onClose={closeSheet}
        />
      )}
      <ConfirmDialog
        open={Boolean(toRemove)}
        danger
        pending={removing}
        title={toRemove ? t('mcpPage.remove.title', { name: toRemove.name }) : ''}
        message={t('mcpPage.remove.body')}
        confirmLabel={t('mcpPage.remove.confirm')}
        pendingLabel={t('mcpPage.remove.pending')}
        onConfirm={remove}
        onCancel={() => setToRemove(null)}
      />
    </section>
  )
}
