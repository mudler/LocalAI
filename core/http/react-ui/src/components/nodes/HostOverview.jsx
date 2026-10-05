import { CapacityGauge } from './ClusterOverview'
import { formatBytes } from './nodeStatus'

// The single-node counterpart of ClusterOverview: the same four capacity
// gauges, fed from this host instead of summed across workers. The lead cell
// trades fleet health, which has nothing to say about one machine, for where
// the machine's memory is going: one bar segment per running model.

const SEGMENTS = 5
const MIN_SEGMENT = 0.8
// The legend grid has three columns: three models, or two and a count.
const LEGEND_COLUMNS = 3

function MemoryShare({ models, ramTotal }) {
  const measured = models
    .filter(model => model.rss_bytes != null && model.rss_bytes > 0)
    .sort((left, right) => right.rss_bytes - left.rss_bytes)
  const modelBytes = measured.reduce((sum, model) => sum + model.rss_bytes, 0)
  const scale = ramTotal > 0 ? ramTotal : modelBytes
  const named = measured.length > LEGEND_COLUMNS ? LEGEND_COLUMNS - 1 : measured.length
  let cursor = 0

  return (
    <div className="fleet-health fleet-overview__cell host-memory" aria-label="Running models summary" aria-live="polite">
      <span className="fleet-kicker">This machine</span>
      <div className="fleet-health__headline">
        <strong>{models.length} running</strong>
        <span>{modelBytes > 0 ? `${formatBytes(modelBytes)} resident${ramTotal > 0 ? ` of ${formatBytes(ramTotal)} RAM` : ''}` : 'no memory readings yet'}</span>
      </div>
      <svg className="fleet-health__bar host-memory__bar" viewBox="0 0 100 4" preserveAspectRatio="none" role="img"
        aria-label={measured.map(model => `${model.model_name} ${formatBytes(model.rss_bytes)}`).join(', ') || 'No models using memory'}>
        <rect className="host-memory__track" x="0" y="0" width="100" height="4" />
        {scale > 0 && measured.map((model, index) => {
          const start = cursor
          // A floor so a model that is small next to the host still shows
          // up as a sliver rather than vanishing; the label carries the size.
          const width = Math.max(MIN_SEGMENT, model.rss_bytes / scale * 100)
          cursor += width
          return <rect key={model.model_name} className={`host-memory__segment host-memory__segment--${index % SEGMENTS}`} x={start} y="0" width={width} height="4"><title>{`${model.model_name}: ${formatBytes(model.rss_bytes)}`}</title></rect>
        })}
      </svg>
      <div className="fleet-health__legend">
        {measured.slice(0, named).map((model, index) => (
          <div key={model.model_name}>
            <span title={model.model_name}><i className={`fleet-health__dot host-memory__dot--${index % SEGMENTS}`} /><span className="host-memory__name">{model.model_name}</span></span>
            <strong>{formatBytes(model.rss_bytes)}</strong>
          </div>
        ))}
        {measured.length > named && <div><span>Others</span><strong>{measured.length - named} more</strong></div>}
      </div>
    </div>
  )
}

export default function HostOverview({ summary, models, ramTotal }) {
  return (
    <section className="fleet-overview host-overview" aria-label="Host overview" data-testid="host-overview">
      <MemoryShare models={models} ramTotal={ramTotal} />
      <CapacityGauge label="VRAM" metric={summary.vram} tone="vram" single noDataText="No GPU detected" />
      <CapacityGauge label="RAM" metric={summary.ram} tone="ram" single />
      <CapacityGauge label="CPU" metric={summary.cpu} cpu tone="cpu" single />
      <CapacityGauge label="Models disk" metric={summary.disk} tone="disk" single />
    </section>
  )
}
