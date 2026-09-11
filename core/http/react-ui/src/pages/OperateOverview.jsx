import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import { ResourceMonitorView } from '../components/ResourceMonitor'
import Sparkline from '../components/Sparkline'
import { useOperateSummary } from '../contexts/OperateSummaryContext'
import { staggerStyle } from '../hooks/useStagger'

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
  const installed = summary?.installed || { backends: null, models: null }

  return (
    <div className="page-pad" data-testid="operate-overview">
      <PageHeader
        title={t('operate.overview.title')}
        supporting={t('operate.overview.subtitle')}
      />

      {/* Always rendered, including at zero. Hiding the grid on a quiet
          installation removed the page's structure exactly when someone was
          most likely to be looking at it, and "0 failed" is information — an
          absent panel is not. The quiet case is said in a line underneath
          rather than by showing nothing. */}
      <dl className="operate-headline reveal-stagger">
        <HeadlineStat
          index={0}
          label={t('operate.overview.headline.requests', { hours: traces?.window_hours || 24 })}
          value={(traces?.total ?? 0).toLocaleString()}
          series={traces?.buckets?.map(b => b.count)}
          tone={traces?.total ? 'primary' : 'muted'}
        />
        <HeadlineStat
          index={1}
          label={t('operate.overview.headline.errors')}
          value={(traces?.errors ?? 0).toLocaleString()}
          series={traces?.buckets?.map(b => b.errors)}
          tone={traces?.errors > 0 ? 'warning' : 'muted'}
        />
        <HeadlineStat
          index={2}
          label={t('operate.overview.headline.p95')}
          value={traces?.total ? `${traces.p95_ms.toLocaleString()} ms` : '—'}
          tone={traces?.total ? 'success' : 'muted'}
        />
        <HeadlineStat
          index={3}
          label={t('operate.overview.headline.host')}
          value={summary?.signals?.host || '—'}
          tone={summary?.signals?.host ? 'primary' : 'muted'}
        />
      </dl>
      {traces && traces.total === 0 && (
        <p className="operate-clear operate-headline__note">{t('operate.overview.headline.quiet')}</p>
      )}

      <section>
        <ResourceMonitorView
          resources={summary?.resources}
          loading={summary?.resourcesLoading}
          unavailable={summary?.resourcesUnavailable}
          title={t('operate.overview.capacity.title')}
          loadingText={t('operate.overview.capacity.loading')}
          unavailableText={t('operate.overview.capacity.unavailable')}
          emptyText={t('operate.overview.capacity.empty')}
          copy={{
            gpuCount: count => t('operate.overview.capacity.gpus', { count }),
            reclaimer: t('operate.overview.capacity.reclaimer'),
            used: t('operate.overview.capacity.used'),
            total: t('operate.overview.capacity.total'),
            systemRam: t('operate.overview.capacity.systemRam'),
            memory: t('operate.overview.capacity.memory'),
            totalVram: t('operate.overview.capacity.totalVram'),
            storage: t('operate.overview.capacity.storage'),
          }}
          testId="operate-capacity"
        />
      </section>

      <section>
        <div className="lane-head"><h2>{t('operate.overview.attention.heading')}</h2></div>
        {attention.length === 0 ? (
          // One line, not a panel. A green reassurance card would make "fine"
          // as loud as "broken", which is the opposite of the point.
          <p className="operate-clear" data-testid="operate-attention-clear">
            {t('operate.overview.attention.clear')}
          </p>
        ) : (
          <ul className="lanes lanes--attention reveal-stagger">
            {attention.map((item, i) => (
              <li key={item.id} data-testid="operate-attention-item" style={staggerStyle(i)}>
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
        <ul className="lanes lanes--sections reveal-stagger">
          <OperateSection
            index={0}
            to="/app/backends"
            label={t('operate.overview.sections.runtime')}
            summary={t('operate.overview.sections.runtimeSummary', {
              backends: installed.backends ?? 0,
              models: installed.models ?? 0,
              updates: upgradeCount,
              running: operations.length,
            })}
          />
          <OperateSection
            index={1}
            to="/app/nodes"
            label={t('operate.overview.sections.cluster')}
            summary={nodes.length
              ? t('operate.overview.sections.clusterSummary', { nodes: nodes.length })
              // "0 nodes" reads as a fault on a single-node install, where the
              // cluster API is simply switched off.
              : t('operate.overview.sections.clusterSingle')}
          />
          <OperateSection
            index={2}
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
            index={3}
            to="/app/settings"
            label={t('operate.overview.sections.administration')}
            summary={t('operate.overview.sections.administrationSummary', {
              memory: summary?.signals?.host || '—',
            })}
          />
        </ul>
      </section>
    </div>
  )
}

function HeadlineStat({ label, value, series, tone, index = 0 }) {
  return (
    <div className="operate-headline__cell" style={staggerStyle(index)}>
      <dt>{label}</dt>
      <dd className={`operate-headline__value operate-headline__value--${tone}`}>{value}</dd>
      {series?.length > 1 && <Sparkline points={series} tone={tone} />}
    </div>
  )
}

function OperateSection({ to, label, summary, index = 0 }) {
  return (
    <li style={staggerStyle(index)}>
      <Link to={to} className="lane">
        <span className="lane__tag">{label}</span>
        <span className="lane__desc">{summary}</span>
        <span className="lane__go" aria-hidden="true">→</span>
      </Link>
    </li>
  )
}
