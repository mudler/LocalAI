/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import Icon from '../components/Icon'
import ActionMenu from '../components/ActionMenu'
import { AppIcon } from '../components/addons/AddonBits'
import { Button, Callout, EmptyState, LoadError, Page, PageHeader, SkeletonRows, StatusChip, Switch } from '../components/ui'
import { chatAgentsApi, chatConnectorsApi } from '../utils/api'
import { bindingAddress, connectionLine, duplicateOf, groupConnectors, rowState } from '../utils/chatConnectors'
import { useAddonIcons } from '../hooks/useAddonIcons'
import '../components/addons/addons.css'
import './connectors.css'

// The messaging connectors of the user. A platform comes from a connector
// add-on, which has its own page to add and edit bindings; this page lists the
// bindings by add-on, switches them on and off, and says when an add-on is
// missing.
export default function ChatConnectors() {
  const { t } = useTranslation('chats')
  const { t: ta } = useTranslation('addons')
  const icons = useAddonIcons()
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const [menuAt, setMenuAt] = useState(null)
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

  const STATE_CHIP = { running: { status: 'running' }, off: { tone: 'idle' }, attention: { tone: 'warn' } }
  const row = (c) => {
    const other = duplicateOf(c)
    const otherHref = other ? bindingAddress(groups, c, other.id) : ''
    const state = rowState(c)
    const chip = STATE_CHIP[state]
    return (
      <li key={c.id} className="cn-row" data-testid="connector-row" data-on={c.enabled ? 'true' : 'false'} data-state={state}>
        <AppIcon src={icons[c.addon] || ''} id={c.addon || c.kind} name={c.name} size={40} />
        <div className="cn-row__main">
          <span className="cn-row__head">
            <span className="cn-row__name">{c.name}</span>
            <StatusChip status={chip.status} tone={chip.tone} label={t(`connectorsPage.state.${state}`)} testId="connector-state" />
          </span>
          <span className="cn-row__meta">
            <span>{ta('connection.answersAs', { name: agentName(c.agent_id) })}</span>
            <span>{connectionLine(c, agents, ta).who}</span>
          </span>
          {other && (
            <span className="cn-result cn-result--warn" data-testid="connector-duplicate">
              <Icon name="warning" />
              <span>
                {t('connectorsPage.duplicate.text', { name: other.name })}
                {otherHref && <> <Link to={otherHref} data-testid="connector-duplicate-open">{t('connectorsPage.duplicate.open', { name: other.name })}</Link></>}
              </span>
            </span>
          )}
          {other && (
            <span className="cn-row__fix">
              <Button size="sm" data-testid="connector-duplicate-off" onClick={() => act(() => chatConnectorsApi.disable(c.id))}>{t('connectorsPage.duplicate.turnOff')}</Button>
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
          <Switch checked={c.enabled} label={t('connectorsPage.onFor', { name: c.name })} testId="connector-switch"
            onChange={(on) => {
              setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
              act(() => (on ? chatConnectorsApi.enable(c.id) : chatConnectorsApi.disable(c.id)))
            }} />
        </span>
      </li>
    )
  }

  return (
    <Page className="cn-page" data-testid="chat-connectors-page">
      <PageHeader
        title={t('connectorsPage.title')}
        supporting={t('connectorsPage.intro')}
        actions={groups.some((g) => g.newHref) && !off && !loadError ? (
          <>
            <Button variant="primary" icon="plus" iconEnd="chevron-down" aria-haspopup="menu" aria-expanded={Boolean(menuAt)} data-testid="connector-new"
              onClick={(e) => { const r = e.currentTarget.getBoundingClientRect(); setMenuAt({ x: r.left, y: r.bottom + 4, returnFocus: e.currentTarget }) }}>
              {t('connectorsPage.new')}
            </Button>
            <ActionMenu at={menuAt} onClose={() => setMenuAt(null)} ariaLabel={t('connectorsPage.newMenu')}
              items={groups.filter((g) => g.newHref).map((g) => ({ key: g.key, icon: 'plus', label: g.title, onClick: () => navigate(g.newHref) }))} />
          </>
        ) : null}
      />

      {error && (
        <Callout tone="error" role="alert" className="cn-gap"
          actions={<Button variant="ghost" size="sm" onClick={() => setError('')}>{t('errors.dismiss')}</Button>}>
          {error}
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
            <Button size="sm" iconEnd="arrow-right" to={g.href}>{t('connectorsPage.manage')}</Button>
          </header>
          {g.items.length === 0
            ? <p className="cn-none">{t('connectorsPage.empty')}</p>
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

      <nav className="cn-related" aria-label={t('mcpPage.related')}>
        <span className="cn-eyebrow">{t('mcpPage.related')}</span>
        <Link to="/app/mcp-connectors">{t('mcpPage.title')}</Link>
        <Link to="/app/chats">{t('mcpPage.chats')}</Link>
      </nav>
    </Page>
  )
}
