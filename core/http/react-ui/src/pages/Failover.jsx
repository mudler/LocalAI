import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import LoadingSpinner from '../components/LoadingSpinner'
import ResponsiveTable from '../components/ResponsiveTable'
import StatusPill from '../components/StatusPill'
import useFailoverChains from '../hooks/useFailoverChains'
import { relative } from '../components/FailoverChainStatus'
import Icon from '../components/Icon'

// Failover: the Operate -> Runtime overview of every failover chain configured
// on this profile. One row per chain: its live state, the target actually
// serving it right now, a compact per-target health strip, and how long the
// active target has held the chain. Sourced from the same hook (GET
// /api/failover, kept live over the /api/failover/events SSE stream) that
// drives the Model Editor's chain strip, so both surfaces always agree.
export default function Failover() {
  const { t, i18n } = useTranslation('models')
  const { chains, loading, error } = useFailoverChains()

  return (
    <div className="page-pad" data-testid="failover-overview">
      <PageHeader
        title={t('failover.overview.title')}
        supporting={t('failover.overview.subtitle')}
      />

      {error && (
        <div className="attention-callout attention-callout--error" role="alert">
          <span>{error}</span>
        </div>
      )}

      {loading ? (
        <LoadingSpinner />
      ) : chains.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><Icon name="shuffle" /></div>
          <h2 className="empty-state-title">{t('failover.overview.empty.title')}</h2>
          <p className="empty-state-text">{t('failover.overview.empty.text')}</p>
          <Link className="btn btn-primary btn-sm" to="/app/model-editor?template=failover">
            {t('failover.overview.empty.cta')}
          </Link>
        </div>
      ) : (
        <ResponsiveTable>
          <thead>
            <tr>
              <th>{t('failover.overview.columns.chain')}</th>
              <th>{t('failover.overview.columns.state')}</th>
              <th>{t('failover.overview.columns.active')}</th>
              <th>{t('failover.overview.columns.targets')}</th>
              <th>{t('failover.overview.columns.changed')}</th>
            </tr>
          </thead>
          <tbody>
            {chains.map(chain => {
              const since = relative(chain.active_since, i18n.language)
              return (
                <tr key={chain.name}>
                  <td>
                    <Link to={`/app/model-editor/${encodeURIComponent(chain.name)}`}>
                      <code>{chain.name}</code>
                    </Link>
                  </td>
                  <td>
                    <StatusPill status={chain.state} label={t(`failover.states.${chain.state}`, chain.state)} />
                  </td>
                  <td><code>{chain.active}</code></td>
                  <td>
                    <span className="failover-overview__targets">
                      {(chain.targets || []).map(target => (
                        <StatusPill
                          key={target.model}
                          status={target.state}
                          label={target.model}
                          className="failover-overview__target-pill"
                        />
                      ))}
                    </span>
                  </td>
                  <td
                    className="text-muted"
                    title={chain.active_since ? new Date(chain.active_since).toLocaleString(i18n.language) : undefined}
                  >
                    {since || '-'}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </ResponsiveTable>
      )}
    </div>
  )
}
