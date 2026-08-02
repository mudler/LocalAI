import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import Sparkline from '../components/Sparkline'
import { useOperateSummary } from '../contexts/OperateSummaryContext'

// The front door to Operate.
//
// Everything here except one block is a summary you could assemble by visiting
// four other pages. The exception is "Needs attention", which is the reason the
// page exists: a single place that is EMPTY when nothing is wrong. That is the
// statement no other page in the app makes.
//
// Not a SplitView. Rail-and-pane is for surfaces where an entity outgrows a row
// and candidates get compared before acting; an overview is read top to bottom
// and clicked through.

const ATTENTION_ROUTE = {
  'backend-update': '/app/backends',
  'operation-failed': '/app/activity',
  'node-unhealthy': '/app/nodes',
}

export default function OperateOverview() {
  const { t } = useTranslation('admin')
  const summary = useOperateSummary()

  const attention = summary?.attention || []
  const nodes = summary?.nodes || []
  const operations = summary?.operations || []
  const upgradeCount = Object.keys(summary?.upgrades || {}).length
  const traces = summary?.traces

  return (
    <div className="page-pad" data-testid="operate-overview">
      <PageHeader
        title={t('operate.overview.title')}
        supporting={t('operate.overview.subtitle')}
      />

      {/* Headline numbers. Only rendered once the summary has answered — an
          installation that has served nothing yet is told so in one line rather
          than shown three zeroes dressed as telemetry. */}
      {traces && (
        traces.total > 0 ? (
          <dl className="operate-headline">
            <HeadlineStat
              label={t('operate.overview.headline.requests', { hours: traces.window_hours })}
              value={traces.total.toLocaleString()}
              series={traces.buckets?.map(b => b.count)}
              tone="primary"
            />
            <HeadlineStat
              label={t('operate.overview.headline.errors')}
              value={traces.errors.toLocaleString()}
              series={traces.buckets?.map(b => b.errors)}
              tone={traces.errors > 0 ? 'warning' : 'muted'}
            />
            <HeadlineStat
              label={t('operate.overview.headline.p95')}
              value={`${traces.p95_ms.toLocaleString()} ms`}
              tone="success"
            />
          </dl>
        ) : (
          <p className="operate-clear">{t('operate.overview.headline.quiet')}</p>
        )
      )}

      <section>
        <div className="lane-head"><h2>{t('operate.overview.attention.heading')}</h2></div>
        {attention.length === 0 ? (
          // One line, not a panel. A green reassurance card would make "fine"
          // as loud as "broken", which is the opposite of the point.
          <p className="operate-clear" data-testid="operate-attention-clear">
            {t('operate.overview.attention.clear')}
          </p>
        ) : (
          <ul className="lanes lanes--attention">
            {attention.map(item => (
              <li key={item.id} data-testid="operate-attention-item">
                <Link to={ATTENTION_ROUTE[item.kind] || '/app/operate'} className="lane">
                  <span className="lane__name">{item.name}</span>
                  <span className="lane__desc">
                    {item.kind === 'backend-update'
                      ? t('operate.overview.attention.backendUpdate', { from: item.from, to: item.to })
                      : item.detail}
                  </span>
                  <span className="lane__go" aria-hidden="true">→</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section>
        <div className="lane-head"><h2>{t('operate.overview.sections.heading')}</h2></div>
        <ul className="lanes lanes--sections">
          <OperateSection
            to="/app/backends"
            label={t('operate.overview.sections.runtime')}
            summary={t('operate.overview.sections.runtimeSummary', {
              updates: upgradeCount,
              running: operations.length,
            })}
          />
          <OperateSection
            to="/app/nodes"
            label={t('operate.overview.sections.cluster')}
            summary={nodes.length
              ? t('operate.overview.sections.clusterSummary', { nodes: nodes.length })
              // "0 nodes" reads as a fault on a single-node install, where the
              // cluster API is simply switched off.
              : t('operate.overview.sections.clusterSingle')}
          />
          <OperateSection
            to="/app/traces"
            label={t('operate.overview.sections.observability')}
            summary={traces?.total
              ? t('operate.overview.sections.observabilityCounted', {
                requests: traces.total.toLocaleString(),
                errors: traces.errors.toLocaleString(),
                p95: traces.p95_ms.toLocaleString(),
              })
              : t('operate.overview.sections.observabilitySummary')}
          />
          <OperateSection
            to="/app/manage"
            label={t('operate.overview.sections.administration')}
            summary={t('operate.overview.sections.administrationSummary')}
          />
        </ul>
      </section>
    </div>
  )
}

function HeadlineStat({ label, value, series, tone }) {
  return (
    <div className="operate-headline__cell">
      <dt>{label}</dt>
      <dd className={`operate-headline__value operate-headline__value--${tone}`}>{value}</dd>
      {series?.length > 1 && <Sparkline points={series} tone={tone} />}
    </div>
  )
}

function OperateSection({ to, label, summary }) {
  return (
    <li>
      <Link to={to} className="lane">
        <span className="lane__tag">{label}</span>
        <span className="lane__desc">{summary}</span>
        <span className="lane__go" aria-hidden="true">→</span>
      </Link>
    </li>
  )
}
