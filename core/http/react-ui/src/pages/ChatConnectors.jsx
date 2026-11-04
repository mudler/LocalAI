/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate, useLocation, useSearchParams } from 'react-router-dom'
import Icon from '../components/Icon'
import ConfirmDialog from '../components/ConfirmDialog'
import OAuthConnection from '../components/connectors/OAuthConnection'
import AddOAuthConnection from '../components/connectors/AddOAuthConnection'
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import { useAuth } from '../context/AuthContext'
import { useListedAddons } from '../addons/useAddonRegistry'
import { chatAgentsApi, chatConnectorsApi } from '../utils/api'
import { acceptsOf, groupConnectors } from '../utils/chatConnectors'
import { isOAuthBinding, oauthErrorText, readOAuthReturn, returnPath, startBody } from '../utils/connectorOAuth'
import './connectors.css'

// The messaging connectors of the user. A platform comes from a connector
// add-on, which has its own page to add and edit bindings; this page lists the
// bindings by add-on, switches them on and off, and says when an add-on is
// missing. A binding that logs in with OAuth also shows its login, and the
// page reads the result of a login it comes back to (?oauth=) once. For a kind
// whose add-on logs in with OAuth and needs no agent, the page adds the
// connection itself, which the person then connects.
export default function ChatConnectors() {
  const { t } = useTranslation('chats')
  const [params, setParams] = useSearchParams()
  const location = useLocation()
  const { isAdmin } = useAuth()
  const apps = useListedAddons()
  const logins = useMemo(() => Object.fromEntries(apps.filter((a) => a.oauth).map((a) => [a.id, a.oauth])), [apps])
  // The result of a login the service sent the browser back with, read once:
  // the query is stripped right away, so a reload does not say it again.
  const [returned, setReturned] = useState(() => readOAuthReturn(location.search))
  const [starting, setStarting] = useState('')
  const [leaving, setLeaving] = useState(null)
  const [disconnecting, setDisconnecting] = useState(false)
  const [notice, setNotice] = useState(null)
  const [kinds, setKinds] = useState(null)
  const [items, setItems] = useState([])
  const [agents, setAgents] = useState([])
  const [off, setOff] = useState('')
  const [loadError, setLoadError] = useState(null)
  const [error, setError] = useState('')

  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'connectors_off') { setOff(e.body.error.message); return true }
    setError(e?.message || t('errors.send'))
    return false
  }, [t])

  // A failed read replaces the lists with an error, so it never looks like
  // "no add-on installed".
  const load = useCallback(async () => {
    try {
      const [k, list] = await Promise.all([chatConnectorsApi.kinds(), chatConnectorsApi.list()])
      setKinds(k.kinds || [])
      setItems(list.connectors || [])
      setOff('')
      setLoadError(null)
    } catch (e) {
      if (e?.status === 409 && e.body?.error?.type === 'connectors_off') { fail(e); return }
      setLoadError(e)
    }
  }, [fail])

  useEffect(() => { load() }, [load])
  useEffect(() => {
    if (returned) setParams(new URLSearchParams(returned.search), { replace: true })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps -- once, on the page the login came back to
  useEffect(() => { chatAgentsApi.list().then((r) => setAgents(r.agents || [])).catch(() => {}) }, [])

  // The MCP connectors used to be a tab of this page.
  if (params.get('kind') === 'mcp') return <Navigate to="/app/mcp-connectors" replace />

  const agentName = (id) => agents.find((a) => a.id === id)?.name || id
  const act = async (fn) => {
    setError('')
    try { await fn(); await load() } catch (e) { fail(e) }
  }
  const { groups, waiting } = groupConnectors(kinds, items)
  const loading = kinds === null && !off && !loadError

  const oauthFail = (e) => {
    const text = oauthErrorText(t, e, 'connectorsPage.oauth.errors')
    if (text) setError(text)
    else fail(e)
  }
  // The browser leaves for the service; the button keeps spinning until it does.
  const connect = async (c, scopes) => {
    setError('')
    setNotice(null)
    setReturned(null)
    setStarting(c.id)
    try {
      const r = await chatConnectorsApi.oauthStart(c.id, startBody(scopes, returnPath(location.pathname, location.search)))
      window.location.assign(r.authorize_url)
    } catch (e) {
      setStarting('')
      oauthFail(e)
    }
  }
  const disconnect = async () => {
    const c = leaving
    setDisconnecting(true)
    setError('')
    setReturned(null)
    try {
      const r = await chatConnectorsApi.oauthDisconnect(c.id)
      setNotice({ tone: r?.revoked ? 'ok' : 'warn', text: t(r?.revoked ? 'connectorsPage.oauth.disconnected' : 'connectorsPage.oauth.disconnectedNotTold', { name: c.name }) })
      await load()
    } catch (e) {
      oauthFail(e)
    } finally {
      setDisconnecting(false)
      setLeaving(null)
    }
  }

  const row = (c) => {
    const a = acceptsOf(c)
    const oauth = isOAuthBinding(c)
    return (
      <li key={c.id} className="cn-row" data-testid="connector-row" data-on={c.enabled ? 'true' : 'false'} data-auth={oauth ? 'oauth2' : undefined}>
        <span className="cn-row__lead" aria-hidden="true"><Icon name={oauth ? 'plug' : 'chat'} /></span>
        <div className="cn-row__main">
          <span className="cn-row__name">{c.name}</span>
          {!c.credentials_only && (
            <span className="cn-row__meta">
              <span>{t('connectorsPage.agentOf', { name: agentName(c.agent_id) })}</span>
              <span>{t('connectorsPage.accepts', { who: a.key === 'listed' ? t('connectorsPage.modes.listed', { count: a.count }) : t(`connectorsPage.modes.${a.key}`) })}</span>
            </span>
          )}
          {c.active === false && (
            <span className="cn-result cn-result--warn">
              <Icon name="warning" />
              <span data-testid="connector-inactive">{t('connectorsPage.inactive', { reason: c.reason })}</span>
            </span>
          )}
        </div>
        <span className="cn-row__actions">
          <label className="dk-choice cn-choice">
            <input type="checkbox" className="dk-check" checked={c.enabled} aria-label={t('connectorsPage.onFor', { name: c.name })}
              onChange={(e) => {
                const on = e.target.checked
                setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
                act(() => (on ? chatConnectorsApi.enable(c.id) : chatConnectorsApi.disable(c.id)))
              }} />
            <span>{t('connectorsPage.on')}</span>
          </label>
        </span>
        {oauth && (
          <OAuthConnection binding={c} login={logins[c.addon] || null} isAdmin={isAdmin}
            starting={starting === c.id} disabled={!!starting && starting !== c.id}
            onConnect={connect} onDisconnect={setLeaving} />
        )}
      </li>
    )
  }

  return (
    <Page className="cn-page" data-testid="chat-connectors-page">
      <PageHeader title={t('connectorsPage.title')} supporting={t('connectorsPage.intro')} />

      {error && (
        <Callout tone="error" role="alert" className="cn-gap"
          actions={<Button variant="ghost" size="sm" onClick={() => setError('')}>{t('errors.dismiss')}</Button>}>
          {error}
        </Callout>
      )}

      {returned && (
        <Callout tone={returned.outcome === 'ok' ? 'ok' : (returned.outcome === 'denied' ? 'info' : 'error')} role={returned.outcome === 'error' ? 'alert' : 'status'}
          className="cn-gap" testId="oauth-returned" data-outcome={returned.outcome}
          actions={<Button variant="ghost" size="sm" onClick={() => setReturned(null)}>{t('errors.dismiss')}</Button>}>
          {returned.outcome === 'error' ? t(`connectorsPage.oauth.returned.error.${returned.classKey}`) : t(`connectorsPage.oauth.returned.${returned.outcome}`)}
        </Callout>
      )}
      {notice && (
        <Callout tone={notice.tone} role="status" className="cn-gap" testId="oauth-notice" data-tone={notice.tone}
          actions={<Button variant="ghost" size="sm" onClick={() => setNotice(null)}>{t('errors.dismiss')}</Button>}>
          {notice.text}
        </Callout>
      )}

      {loading && <SkeletonRows count={3} />}
      {loadError && <LoadError title={t('connectorsPage.loadFailed')} error={loadError} onRetry={load} />}
      {off && <EmptyState variant="unavailable" title={off} testId="connectors-off" />}

      {kinds !== null && !off && !loadError && groups.length === 0 && (
        <EmptyState
          title={t('connectorsPage.noAddon')}
          body={t('connectorsPage.noAddonHint')}
          actions={<Button variant="primary" icon="puzzle" to="/app/addons">{t('connectorsPage.gallery')}</Button>}
          testId="no-connector-addon"
        />
      )}

      {!loadError && groups.map((g) => (
        <section key={g.key} className="cn-group" data-testid="connector-group" aria-label={g.title}>
          <header className="cn-group__head">
            <div className="cn-group__text">
              <h2 className="cn-group__title">{g.title}</h2>
              {g.description && <p className="cn-group__desc">{g.description}</p>}
            </div>
            <span className="cn-group__actions">
              <Button size="sm" iconEnd="arrow-right" to={g.href}>{t(g.addsLogin ? 'connectorsPage.oauth.add.manage' : 'connectorsPage.manage')}</Button>
            </span>
          </header>
          {g.addsLogin && (
            <AddOAuthConnection key={g.key} kind={g} create={chatConnectorsApi.create}
              onAdded={async (b) => {
                setError('')
                setReturned(null)
                setNotice({ tone: 'ok', text: t('connectorsPage.oauth.add.added', { name: b?.name || '' }) })
                await load()
              }} />
          )}
          {g.items.length === 0
            ? <p className="cn-none">{t(g.addsLogin ? 'connectorsPage.oauth.add.empty' : 'connectorsPage.empty')}</p>
            : <ul className="cn-list">{g.items.map(row)}</ul>}
        </section>
      ))}

      {!loadError && waiting.length > 0 && (
        <section className="cn-group" data-testid="connector-waiting" aria-labelledby="connector-waiting-title">
          <header className="cn-group__head">
            <div className="cn-group__text">
              <h2 className="cn-group__title" id="connector-waiting-title">{t('connectorsPage.waitingTitle')}</h2>
              <p className="cn-group__desc">{t('connectorsPage.waitingText')}</p>
            </div>
            <Button size="sm" icon="puzzle" to="/app/addons">{t('connectorsPage.gallery')}</Button>
          </header>
          <ul className="cn-list">{waiting.map(row)}</ul>
        </section>
      )}

      <ConfirmDialog
        open={!!leaving}
        title={leaving ? t('connectorsPage.oauth.disconnectTitle', { name: leaving.name }) : ''}
        message={t('connectorsPage.oauth.disconnectText')}
        confirmLabel={t('connectorsPage.oauth.disconnect')}
        pendingLabel={t('connectorsPage.oauth.disconnecting')}
        danger
        pending={disconnecting}
        onConfirm={disconnect}
        onCancel={() => { if (!disconnecting) setLeaving(null) }}
      />

      <nav className="cn-related" aria-label={t('mcpPage.related')}>
        <span className="cn-eyebrow">{t('mcpPage.related')}</span>
        <Link to="/app/mcp-connectors">{t('mcpPage.title')}</Link>
        <Link to="/app/chats">{t('mcpPage.chats')}</Link>
      </nav>
    </Page>
  )
}
