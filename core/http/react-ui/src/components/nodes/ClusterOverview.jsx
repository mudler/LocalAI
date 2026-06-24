import { formatCapacity } from './nodeStatus'

const ATTENTION = [
  ['all', 'Needs attention'],
  ['pending', 'Pending approval'],
  ['offlineOrUnhealthy', 'Offline / unhealthy'],
  ['lowVRAM', 'Low VRAM'],
  ['lowRAM', 'Low RAM'],
  ['lowDisk', 'Low models disk'],
]

function CapacityGauge({ label, metric, cpu = false, tone }) {
  const reporting = metric.reportingCount > 0
  const percent = reporting ? Math.round(metric.usagePercent) : 0
  const value = cpu
    ? `${Number(metric.busyCoreEquivalents.toFixed(1))} busy / ${metric.totalLogicalCores} cores`
    : formatCapacity(metric.used, metric.total)
  const available = cpu
    ? `${Number(metric.idleCoreEquivalents.toFixed(1))} idle · load ${metric.load1.toFixed(2)}`
    : reporting ? `${formatCapacity(metric.available, metric.total).split(' / ')[0]} available` : 'No data'

  return (
    <article className={`fleet-gauge fleet-gauge--${tone} fleet-overview__cell`} aria-label={`${label} capacity`}>
      <div className="fleet-kicker">{label} capacity</div>
      <div className="fleet-gauge__graphic" aria-hidden="true">
        <svg viewBox="0 0 100 54">
          <path className="fleet-gauge__track" d="M7 50 A43 43 0 0 1 93 50" pathLength="100" />
          {reporting && <path className="fleet-gauge__value" d="M7 50 A43 43 0 0 1 93 50" pathLength="100" strokeDasharray={`${percent} 100`} />}
        </svg>
        <strong>{reporting ? `${percent}%` : '—'}</strong>
      </div>
      <div className="fleet-gauge__value-text">{reporting ? value : 'No data'}</div>
      <div className="fleet-gauge__detail">{available}</div>
      <span className="sr-only">Capacity coverage: {metric.reportingCount} of {metric.reportingCount + metric.unknownCount} nodes reporting; {metric.unknownCount} unknown.</span>
      {metric.unknownCount > 0 && <div className="fleet-gauge__coverage">{metric.unknownCount} node{metric.unknownCount === 1 ? '' : 's'} unavailable</div>}
    </article>
  )
}

export default function ClusterOverview({ summary, activeAttention, onAttentionSelect }) {
  const { health } = summary
  const total = Math.max(health.total, 1)
  const segments = [
    ['healthy', health.healthy],
    ['draining', health.draining],
    ['unhealthy', health.pending + health.unhealthy + health.offline + health.other],
  ]
  let cursor = 0

  return <>
    <section className="fleet-overview" aria-label="Fleet overview">
      <div className="fleet-health fleet-overview__cell" aria-label="Fleet health summary" aria-live="polite">
        <span className="fleet-kicker">Fleet health</span>
        <div className="fleet-health__headline"><strong>{health.healthy} healthy</strong><span>of {health.total} nodes</span></div>
        <svg className="fleet-health__bar" viewBox="0 0 100 4" preserveAspectRatio="none" aria-hidden="true">
          {segments.map(([status, count]) => {
            const start = cursor
            const width = count / total * 100
            cursor += width
            return <rect key={status} className={`fleet-health__segment fleet-health__segment--${status}`} x={start} y="0" width={width} height="4" />
          })}
        </svg>
        <div className="fleet-health__legend">
          {segments.map(([status, count]) => {
            const label = status === 'unhealthy' ? 'Attention' : status[0].toUpperCase() + status.slice(1)
            const percentage = health.total ? (count / health.total * 100).toFixed(1) : '0.0'
            return <div key={status}><span><i className={`fleet-health__dot fleet-health__dot--${status}`} />{label}</span><strong>{count} · {percentage}%</strong></div>
          })}
        </div>
      </div>

      <CapacityGauge label="VRAM" metric={summary.vram} tone="vram" />
      <CapacityGauge label="RAM" metric={summary.ram} tone="ram" />
      <CapacityGauge label="CPU" metric={summary.cpu} cpu tone="cpu" />
      <CapacityGauge label="Models disk" metric={summary.disk} tone="disk" />

    </section>
    <aside className="fleet-attention" aria-label="Attention queue">
      <span className="fleet-attention__title"><i className="fas fa-triangle-exclamation" aria-hidden="true" /><strong>{summary.attentionNodeCount} node{summary.attentionNodeCount === 1 ? '' : 's'} need attention</strong></span>
      <div className="fleet-attention__filters">
        {ATTENTION.map(([key, label]) => {
          const count = key === 'all' ? summary.attentionNodeCount : summary.attention[key].length
          return (
            <button key={key} type="button" className={`fleet-attention__filter${activeAttention === key ? ' is-active' : ''}`}
              aria-pressed={activeAttention === key} onClick={() => onAttentionSelect(activeAttention === key ? null : key)}>
              {label} <strong>{count}</strong>
            </button>
          )
        })}
      </div>
    </aside>
  </>
}
