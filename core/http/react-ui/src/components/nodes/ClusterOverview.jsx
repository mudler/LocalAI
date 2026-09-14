import { formatCapacity } from './nodeStatus'

const ATTENTION = [
  ['all', 'Needs attention'],
  ['pending', 'Pending approval'],
  ['offlineOrUnhealthy', 'Offline / unhealthy'],
  ['lowVRAM', 'Low VRAM'],
  ['lowRAM', 'Low RAM'],
  ['lowDisk', 'Low models disk'],
]

function CapacityGauge({ label, metric, cpu = false }) {
  const reporting = metric.reportingCount > 0
  const percent = reporting ? Math.round(metric.usagePercent) : 0
  const value = cpu
    ? `${Number(metric.busyCoreEquivalents.toFixed(1))} busy / ${metric.totalLogicalCores} cores`
    : formatCapacity(metric.used, metric.total)
  const available = cpu
    ? `${Number(metric.idleCoreEquivalents.toFixed(1))} idle · load ${metric.load1.toFixed(2)}`
    : reporting ? `${formatCapacity(metric.available, metric.total).split(' / ')[0]} available` : 'No data'

  return (
    <article className="fleet-gauge" aria-label={`${label} capacity`}>
      <div className="fleet-gauge__graphic" aria-hidden="true">
        <svg viewBox="0 0 120 66">
          <path className="fleet-gauge__track" d="M10 60 A50 50 0 0 1 110 60" pathLength="100" />
          {reporting && <path className="fleet-gauge__value" d="M10 60 A50 50 0 0 1 110 60" pathLength="100" strokeDasharray={`${percent} 100`} />}
        </svg>
        <strong>{reporting ? `${percent}%` : '—'}</strong>
      </div>
      <div className="fleet-gauge__label">{label}</div>
      <div className="fleet-gauge__value-text">{reporting ? value : 'No data'}</div>
      <div className="fleet-gauge__detail">{available}</div>
      <div className="fleet-gauge__coverage">{metric.reportingCount} reporting · {metric.unknownCount} unknown</div>
    </article>
  )
}

export default function ClusterOverview({ summary, activeAttention, onAttentionSelect }) {
  const { health } = summary
  const total = Math.max(health.total, 1)
  const segments = [
    ['healthy', health.healthy],
    ['draining', health.draining],
    ['pending', health.pending],
    ['unhealthy', health.unhealthy + health.offline + health.other],
  ]
  let cursor = 0

  return (
    <section className="fleet-overview" aria-label="Fleet overview">
      <div className="fleet-health" aria-label="Fleet health summary" aria-live="polite">
        <div className="fleet-health__heading">
          <div><span className="fleet-kicker">Fleet health</span><strong>{health.total} nodes</strong></div>
          <span>{health.healthy} healthy · {health.draining} draining · {health.pending} pending · {health.offline + health.unhealthy} impaired</span>
        </div>
        <svg className="fleet-health__bar" viewBox="0 0 100 4" preserveAspectRatio="none" aria-hidden="true">
          {segments.map(([status, count]) => {
            const start = cursor
            const width = count / total * 100
            cursor += width
            return <rect key={status} className={`fleet-health__segment fleet-health__segment--${status}`} x={start} y="0" width={width} height="4" />
          })}
        </svg>
      </div>

      <div className="fleet-gauges">
        <CapacityGauge label="VRAM" metric={summary.vram} />
        <CapacityGauge label="RAM" metric={summary.ram} />
        <CapacityGauge label="CPU" metric={summary.cpu} cpu />
        <CapacityGauge label="Models disk" metric={summary.disk} />
      </div>

      <div className="fleet-attention" aria-label="Attention queue">
        <span className="fleet-kicker">Attention queue</span>
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
      </div>
    </section>
  )
}
