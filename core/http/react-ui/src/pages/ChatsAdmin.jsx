import { useCallback, useEffect, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { chatAdminApi } from '../utils/api'
import { useHubAuth } from '../components/hub/useHubAuth'
// eslint-disable-next-line no-unused-vars
import { StatusMark, formatWhen } from '../components/agents/AgentBits'
// eslint-disable-next-line no-unused-vars
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../components/LoadingSpinner'
import './agents.css'
import './agent-jobs.css'

// The administrator's view of every user's saved agents, triggers and
// messaging connectors (/api/chats/admin/*): the owner of each, and the two
// things an administrator may do to another user's item, switch it off and
// delete it. Instructions, prompts and tokens are not shown; the server does
// not return them. A section is left out when its feature is off on the server.
export default function ChatsAdmin() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('agents')
  const { features, featuresLoaded } = useHubAuth()
  const [data, setData] = useState({ agents: [], triggers: [], connectors: [] })
  const [loading, setLoading] = useState(true)
  const [runs, setRuns] = useState({}) // trigger id -> runs
  const [toDelete, setToDelete] = useState(null) // { kind, item }

  const on = (name) => features[name] !== false
  const load = useCallback(async () => {
    if (!featuresLoaded) return
    const read = (want, call, key) => (want ? call().then(r => r[key] || []).catch(err => { addToast(t('admin.loadFailed', { message: err.message }), 'error'); return [] }) : Promise.resolve([]))
    const [agents, triggers, connectors] = await Promise.all([
      read(on('agents'), chatAdminApi.agents, 'agents'),
      read(on('triggers'), chatAdminApi.triggers, 'triggers'),
      read(on('connectors'), chatAdminApi.connectors, 'connectors'),
    ])
    setData({ agents, triggers, connectors })
    setLoading(false)
  // features is read through on(); it changes only when featuresLoaded does.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [featuresLoaded, addToast, t])
  useEffect(() => { load() }, [load])

  const act = async (fn, done) => {
    try {
      await fn()
      addToast(done, 'success')
      load()
    } catch (err) {
      addToast(t('admin.actionFailed', { message: err.message }), 'error')
    }
  }
  const showRuns = async (id) => {
    if (runs[id]) { setRuns(r => ({ ...r, [id]: undefined })); return }
    try {
      const res = await chatAdminApi.triggerRuns(id)
      setRuns(r => ({ ...r, [id]: res.runs || [] }))
    } catch (err) {
      addToast(t('admin.loadFailed', { message: err.message }), 'error')
    }
  }
  const remove = ({ kind, item }) => {
    setToDelete(null)
    const call = kind === 'agent' ? chatAdminApi.deleteAgent : kind === 'trigger' ? chatAdminApi.deleteTrigger : chatAdminApi.deleteConnector
    act(() => call(item.id), t('admin.deleted', { name: item.name }))
  }

  const table = (id, head, rows) => (
    <div className="dk-table-wrap aj-wrap" role="region" aria-labelledby={id} tabIndex={0}>
      <table className="dk-table aj-table">
        <thead><tr>{head.map((h, i) => <th key={i} scope="col">{h}</th>)}</tr></thead>
        <tbody>{rows}</tbody>
      </table>
    </div>
  )

  if (loading) return <div className="page page--wide loading-center"><LoadingSpinner size="lg" /></div>

  return (
    <div className="page page--wide ag-page aj-page" data-testid="chats-admin-page">
      <header className="aj-head"><h1 className="aj-title">{t('admin.title')}</h1></header>
      <p className="aj-sum">{t('admin.lede')}</p>

      {on('agents') && (
        <section className="aj-sec" data-testid="admin-agents">
          <h2 className="ag-eyebrow" id="adm-agents">{t('admin.agents', { count: data.agents.length })}</h2>
          {data.agents.length === 0 ? <p className="ag-note">{t('admin.noneAgents')}</p> : table('adm-agents', [t('admin.owner'), t('admin.name'), t('admin.model'), ''], data.agents.map(a => (
            <tr key={a.id} data-testid="admin-agent-row">
              <td className="aj-mono">{a.owner}</td>
              <td><span className="aj-name">{a.name}</span><span className="aj-sub"><span className="aj-sub__text">{a.description}</span></span></td>
              <td className="aj-mono">{a.model}</td>
              <td className="dk-table-actions"><button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setToDelete({ kind: 'agent', item: a })}>{t('admin.delete')}</button></td>
            </tr>
          )))}
        </section>
      )}

      {on('triggers') && (
        <section className="aj-sec" data-testid="admin-triggers">
          <h2 className="ag-eyebrow" id="adm-triggers">{t('admin.triggers', { count: data.triggers.length })}</h2>
          {data.triggers.length === 0 ? <p className="ag-note">{t('admin.noneTriggers')}</p> : table('adm-triggers', [t('admin.owner'), t('admin.name'), t('admin.state'), ''], data.triggers.flatMap(tr => [
            <tr key={tr.id} data-testid="admin-trigger-row">
              <td className="aj-mono">{tr.owner}</td>
              <td><span className="aj-name">{tr.name}</span><span className="aj-sub"><span className="aj-sub__text">{tr.summary}</span></span></td>
              <td><StatusMark status={tr.enabled ? 'ready' : 'stopped'} label={tr.enabled ? t('admin.on') : t('admin.off')} /></td>
              <td className="dk-table-actions">
                <span className="aj-acts">
                  <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" aria-expanded={!!runs[tr.id]} onClick={() => showRuns(tr.id)}>{t('admin.runs')}</button>
                  {tr.enabled && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => act(() => chatAdminApi.disableTrigger(tr.id), t('admin.disabled', { name: tr.name }))}>{t('admin.disable')}</button>}
                  <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setToDelete({ kind: 'trigger', item: tr })}>{t('admin.delete')}</button>
                </span>
              </td>
            </tr>,
            runs[tr.id] && (
              <tr key={`${tr.id}-runs`} className="dk-table-detail">
                <td colSpan={4}>
                  <div className="dk-table-detail-body">
                  {runs[tr.id].length === 0 ? <p className="ag-note">{t('admin.noRuns')}</p> : (
                    <ul className="aj-steps" data-testid="admin-trigger-runs">
                      {runs[tr.id].map(r => <li key={r.id}>{formatWhen(r.started || r.due)} · {r.status}{r.reason ? ` · ${r.reason}` : ''}</li>)}
                    </ul>
                  )}
                  </div>
                </td>
              </tr>
            ),
          ]))}
        </section>
      )}

      {on('connectors') && (
        <section className="aj-sec" data-testid="admin-connectors">
          <h2 className="ag-eyebrow" id="adm-connectors">{t('admin.connectors', { count: data.connectors.length })}</h2>
          {data.connectors.length === 0 ? <p className="ag-note">{t('admin.noneConnectors')}</p> : table('adm-connectors', [t('admin.owner'), t('admin.name'), t('admin.state'), ''], data.connectors.map(c => (
            <tr key={c.id} data-testid="admin-connector-row">
              <td className="aj-mono">{c.owner}</td>
              <td><span className="aj-name">{c.name}</span><span className="aj-sub"><span>{c.kind}</span></span></td>
              <td><StatusMark status={c.enabled ? 'ready' : 'stopped'} label={c.enabled ? t('admin.on') : t('admin.off')} /></td>
              <td className="dk-table-actions">
                <span className="aj-acts">
                  {c.enabled && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => act(() => chatAdminApi.disableConnector(c.id), t('admin.disabled', { name: c.name }))}>{t('admin.disable')}</button>}
                  <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setToDelete({ kind: 'connector', item: c })}>{t('admin.delete')}</button>
                </span>
              </td>
            </tr>
          )))}
        </section>
      )}

      <ChatDeleteConfirm
        item={toDelete?.item || null}
        kind={toDelete?.kind || 'agent'}
        onConfirm={() => remove(toDelete)}
        onCancel={() => setToDelete(null)}
      />
    </div>
  )
}
