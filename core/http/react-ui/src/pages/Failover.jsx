/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import LoadingSpinner from '../components/LoadingSpinner'
import StatusPill from '../components/StatusPill'
import useFailoverChains from '../hooks/useFailoverChains'
import { relative } from '../components/FailoverChainStatus'
import { useNodeList, useReplicas, useRules } from '../hooks/useSwarm'
import { isDown, isPlaceable, leavePreview, singleReplicaModels } from '../utils/swarm'
import { timeAgo } from '../components/nodes/nodeStatus'
import LeaveList from '../components/swarm/LeaveList'
import Icon from '../components/Icon'
import './swarm.css'

const POLICY = ['notAnswering', 'requests', 'replicas', 'back']

// Failover has two halves. Model failover chains are a feature of the model
// config: one name served by an ordered list of targets, with the live health
// of each, over GET /api/failover and its event stream. Node failover is what
// the router and the health monitor already do when a worker stops answering;
// the page says it in words and, for each node, works out from the loaded
// replicas and the rules what would stop if it went, labelled as a preview.
//
// A single install has chains and no nodes, so it shows only the chains.
export default function Failover() {
  const { t, i18n } = useTranslation('models')
  const { t: ts } = useTranslation('swarm')
  const { chains, loading, error } = useFailoverChains()
  const { nodes, status } = useNodeList({ interval: 10000 })
  const replicas = useReplicas()
  const { rules } = useRules()
  const [picked, setPicked] = useState(null)
  const cluster = status === 'ready' && nodes.length > 0

  const loadReplicas = replicas.load
  useEffect(() => { if (cluster) void loadReplicas() }, [cluster, loadReplicas])

  const candidates = useMemo(() => nodes.filter(node => (node.node_type || 'backend') === 'backend' && node.status !== 'pending'), [nodes])
  const lost = nodes.filter(node => isDown(node))
  // Start on the healthy node that holds the most models: it is the one whose
  // loss costs the most, so it is the one worth reading first.
  const busiest = [...candidates].filter(isPlaceable).sort((a, b) => ((b.model_count || 0) - (a.model_count || 0)) || ((b.in_flight_count || 0) - (a.in_flight_count || 0)))[0]
  const subject = candidates.find(node => node.id === picked) || busiest || candidates[0] || null
  const rows = replicas.state === 'loaded' ? replicas.rows : null
  const preview = useMemo(
    () => (subject && rows ? leavePreview({ node: subject, rows, nodes, rules }) : null),
    [subject, rows, nodes, rules],
  )
  const single = useMemo(() => (rows ? singleReplicaModels(rows, nodes) : []), [rows, nodes])

  return (
    <div className="page page--wide sw-page" data-testid="failover-overview">
      <h1 className="dk-sr-only">{t('failover.overview.title')}</h1>
      <p className="sw-note">{ts('failover.lede')}</p>

      {cluster && lost.length > 0 && (
        <section className="sw-banner" data-level="error" role="status" data-testid="failover-lost">
          <p>
            <strong>{ts('failover.lost', { names: lost.map(n => n.name).join(', '), count: lost.length })}</strong>{' '}
            {ts('failover.lostSub', { when: lost[0].last_heartbeat ? timeAgo(lost[0].last_heartbeat) : ts('detail.never') })}{' '}
            <Link className="dk-link" to={`/app/nodes/${encodeURIComponent(lost[0].id)}`}>{ts('failover.openNode')}</Link>
          </p>
        </section>
      )}

      {cluster && (
        <section className="sw-section" aria-labelledby="sw-fo-policy">
          <h2 className="sw-h2" id="sw-fo-policy">{ts('failover.policyTitle')}</h2>
          <dl className="sw-policy">
            {POLICY.map(key => (
              <div key={key} className="sw-policy__row">
                <dt>{ts(`failover.policy.${key}.title`)}</dt>
                <dd>{ts(`failover.policy.${key}.text`)}</dd>
              </div>
            ))}
          </dl>
          <p className="sw-note sw-note--quiet">{ts('failover.policyNote')}</p>
        </section>
      )}

      <section className="sw-section" aria-labelledby="sw-fo-chains">
        <h2 className="sw-h2" id="sw-fo-chains">{t('failover.overview.title')} <span className="sw-count dk-mono">{chains.length}</span></h2>
        <p className="sw-note sw-note--quiet">{t('failover.overview.subtitle')}</p>

        {error && (
          <div className="sw-error" role="alert"><Icon name="alert-circle" /><span>{error}</span></div>
        )}

        {loading ? (
          <div className="sw-loading"><LoadingSpinner /></div>
        ) : chains.length === 0 ? (
          <div className="dk-empty">
            <div className="dk-empty-icon"><Icon name="shuffle" /></div>
            <h3 className="dk-empty-title">{t('failover.overview.empty.title')}</h3>
            <p className="dk-empty-text">{t('failover.overview.empty.text')}</p>
            <Link className="dk-btn dk-btn--primary" to="/app/model-editor?template=failover">{t('failover.overview.empty.cta')}</Link>
          </div>
        ) : (
          <div className="dk-table-wrap sw-wrap">
            <table className="dk-table sw-table">
              <caption className="dk-sr-only">{t('failover.overview.title')}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('failover.overview.columns.chain')}</th>
                  <th scope="col">{t('failover.overview.columns.state')}</th>
                  <th scope="col" className="dk-hide-phone">{t('failover.overview.columns.active')}</th>
                  <th scope="col">{t('failover.overview.columns.targets')}</th>
                  <th scope="col" className="dk-hide-phone">{t('failover.overview.columns.changed')}</th>
                </tr>
              </thead>
              <tbody>
                {chains.map(chain => {
                  const since = relative(chain.active_since, i18n.language)
                  return (
                    <tr key={chain.name} data-row>
                      <td>
                        <Link className="dk-table-name dk-mono sw-name" to={`/app/model-editor/${encodeURIComponent(chain.name)}`}><code>{chain.name}</code></Link>
                      </td>
                      <td><StatusPill status={chain.state} label={t(`failover.states.${chain.state}`, chain.state)} /></td>
                      <td className="dk-hide-phone"><code className="dk-mono">{chain.active}</code></td>
                      <td>
                        <span className="failover-overview__targets">
                          {(chain.targets || []).map(target => (
                            <StatusPill key={target.model} status={target.state} label={target.model} className="failover-overview__target-pill" />
                          ))}
                        </span>
                      </td>
                      <td className="dk-hide-phone dk-mono" title={chain.active_since ? new Date(chain.active_since).toLocaleString(i18n.language) : undefined}>
                        {since || '-'}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {cluster && candidates.length > 0 && (
        <section className="sw-section" aria-labelledby="sw-fo-try" data-testid="failover-try">
          <h2 className="sw-h2" id="sw-fo-try">{ts('failover.tryTitle')} <span className="sw-badge-preview">{ts('preview.label')}</span></h2>
          <div className="dk-segmented" role="radiogroup" aria-label={ts('failover.tryPick')}>
            {candidates.slice(0, 8).map(node => (
              <button key={node.id} type="button" role="radio" aria-checked={subject?.id === node.id} className="dk-seg dk-mono" onClick={() => setPicked(node.id)}>{node.name}</button>
            ))}
          </div>
          {preview === null
            ? <p className="sw-note">{replicas.state === 'error' ? ts('leave.unavailable') : ts('failover.tryLoading')}</p>
            : <LeaveList items={preview} lost />}
          <p className="sw-note sw-note--quiet">{ts('leave.how')}</p>

          {single.length > 0 && (
            <div className="sw-single" data-testid="failover-single">
              <p>
                <strong>{ts('failover.single', { count: single.length })}</strong>{' '}
                {ts('failover.singleSub')}
              </p>
              <ul className="sw-single__list">
                {single.slice(0, 6).map(model => (
                  <li key={model}>
                    <span className="dk-mono">{model}</span>
                    <Link className="dk-link" to={`/app/scheduling?new=${encodeURIComponent(model)}&min=2`}>{ts('failover.addRule')}</Link>
                  </li>
                ))}
              </ul>
              {single.length > 6 && <p className="sw-note sw-note--quiet">{ts('failover.singleMore', { count: single.length - 6 })}</p>}
            </div>
          )}
        </section>
      )}
    </div>
  )
}
