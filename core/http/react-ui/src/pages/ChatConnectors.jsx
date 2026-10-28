/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate, useSearchParams } from 'react-router-dom'
import { chatAgentsApi, chatConnectorsApi } from '../utils/api'
import { acceptsOf, groupConnectors } from '../utils/chatConnectors'
import './chatskills.css'
import './chatauto.css'

// The messaging connectors of the user. A platform comes from a connector
// add-on, which has its own page to add and edit bindings; this page lists the
// bindings by add-on, switches them on and off, and says when an add-on is
// missing.
export default function ChatConnectors() {
  const { t } = useTranslation('chats')
  const [params] = useSearchParams()
  const [kinds, setKinds] = useState(null)
  const [items, setItems] = useState([])
  const [agents, setAgents] = useState([])
  const [off, setOff] = useState('')
  const [error, setError] = useState('')

  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'connectors_off') { setOff(e.body.error.message); return }
    setError(e?.message || t('errors.send'))
  }, [t])

  const load = useCallback(async () => {
    try {
      const [k, list] = await Promise.all([chatConnectorsApi.kinds(), chatConnectorsApi.list()])
      setKinds(k.kinds || [])
      setItems(list.connectors || [])
      setOff('')
    } catch (e) { fail(e) }
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

  const row = (c) => {
    const a = acceptsOf(c)
    return (
      <li key={c.id} className="cs-row" data-testid="connector-row">
        <div className="cs-row__text">
          <span className="cs-row__name">{c.name}</span>
          <span className="cs-muted">
            {t('connectorsPage.agentOf', { name: agentName(c.agent_id) })}
            {' · '}
            {t('connectorsPage.accepts', { who: a.key === 'listed' ? t('connectorsPage.modes.listed', { count: a.count }) : t(`connectorsPage.modes.${a.key}`) })}
          </span>
          {c.active === false && <span className="cs-muted" data-testid="connector-inactive">{t('connectorsPage.inactive', { reason: c.reason })}</span>}
        </div>
        <span className="cs-row__actions">
          <label className="ca-radio">
            <input type="checkbox" checked={c.enabled} aria-label={t('connectorsPage.onFor', { name: c.name })}
              onChange={(e) => {
                const on = e.target.checked
                setItems((list) => list.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)))
                act(() => (on ? chatConnectorsApi.enable(c.id) : chatConnectorsApi.disable(c.id)))
              }} />
            <span>{t('connectorsPage.on')}</span>
          </label>
        </span>
      </li>
    )
  }

  return (
    <div className="cs-page" data-testid="chat-connectors-page">
      <header className="cs-head">
        <h1 className="cs-title">{t('connectorsPage.title')}</h1>
      </header>
      <p className="cs-muted">{t('connectorsPage.intro')}</p>
      {off && <div className="cs-notice" role="note">{off}</div>}
      {error && (
        <div className="cs-error" role="alert">
          <span>{error}</span>
          <button type="button" className="cs-link" onClick={() => setError('')}>{t('errors.dismiss')}</button>
        </div>
      )}

      {kinds !== null && !off && groups.length === 0 && (
        <div className="cs-notice" role="note" data-testid="no-connector-addon">
          <strong>{t('connectorsPage.noAddon')}</strong>
          <p className="cs-muted">{t('connectorsPage.noAddonHint')}</p>
          <Link className="btn btn-secondary btn-sm" to="/app/addons">{t('connectorsPage.gallery')}</Link>
        </div>
      )}

      {groups.map((g) => (
        <section key={g.key} className="cs-group" data-testid="connector-group" aria-label={g.title}>
          <header className="cs-head">
            <h2 className="cs-sub">{g.title}</h2>
            <Link className="btn btn-secondary btn-sm" to={g.href}>{t('connectorsPage.manage')}</Link>
          </header>
          {g.description && <p className="cs-muted">{g.description}</p>}
          {g.items.length === 0 ? <p className="cs-muted">{t('connectorsPage.empty')}</p> : <ul className="cs-list">{g.items.map(row)}</ul>}
        </section>
      ))}

      {waiting.length > 0 && (
        <section className="cs-group" data-testid="connector-waiting">
          <ul className="cs-list">{waiting.map(row)}</ul>
          <Link className="btn btn-secondary btn-sm" to="/app/addons">{t('connectorsPage.gallery')}</Link>
        </section>
      )}

      <p className="cs-muted">
        <Link to="/app/mcp-connectors">{t('connectorsPage.mcpLink')}</Link>
        {' · '}
        <Link to="/app/chats">{t('skillsPage.back')}</Link>
      </p>
    </div>
  )
}
