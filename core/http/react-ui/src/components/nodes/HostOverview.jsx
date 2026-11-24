/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { formatBytes, formatCapacity } from './nodeStatus'
import { cssVars, gbLabel } from '../../utils/modelLedger'

// The single-node view of capacity: where the GPU memory stands, what the
// running models hold in host memory, and the four readings of the machine
// (VRAM, RAM, CPU, models disk). The numbers are the host's own, read through
// the same fleet maths a cluster uses, so the two never disagree.
//
// LocalAI reports GPU memory per device and resident memory per model process.
// It does not report GPU memory per model, so no model is shown with a GPU size.

const SERIES = 6
// The legend lists three models, or two and a count.
const LEGEND_MAX = 3

function GpuStrip({ resources, summary }) {
  const { t } = useTranslation('operate')
  const gpus = Array.isArray(resources?.gpus) ? resources.gpus : []
  const reading = summary.vram
  if (resources && !(reading.reportingCount > 0)) {
    return (
      <div className="op-strip" data-testid="gpu-strip">
        <span className="dk-eyebrow">{t('machine.gpuMemory')}</span>
        <p className="op-strip__line">{t('machine.noGpu')}</p>
      </div>
    )
  }
  if (!resources) return null
  const pct = Math.round(reading.usagePercent)
  return (
    <div className="op-strip" data-testid="gpu-strip">
      <span className="dk-eyebrow">{t('machine.gpuMemoryUsed', { used: gbLabel(reading.used), total: gbLabel(reading.total) })}</span>
      <div
        className="dk-meter"
        role="img"
        aria-label={t('machine.gpuMeter', { used: gbLabel(reading.used), total: gbLabel(reading.total), percent: pct })}
      >
        <span
          className={`dk-meter-seg${pct >= 97 ? ' dk-meter-seg--error' : pct >= 90 ? ' dk-meter-seg--warn' : ''}`}
          style={cssVars({ '--dk-w': `${Math.min(100, reading.usagePercent).toFixed(1)}%` })}
        />
      </div>
      <p className="op-strip__line">
        <strong>{t('machine.gpuFree', { free: gbLabel(reading.available) })}</strong> {t('machine.gpuFreeHint')}
      </p>
      {gpus.length > 1 && (
        <ul className="op-gpus" aria-label={t('machine.gpus')}>
          {gpus.map((gpu, index) => {
            const used = Math.max(0, Number(gpu.used_vram) || 0)
            const total = Math.max(0, Number(gpu.total_vram) || 0)
            return (
              <li key={gpu.index ?? index}>
                <span className="op-gpus__name">{gpu.name || t('machine.gpuN', { n: index + 1 })}</span>
                <span className="dk-meter op-gpus__meter" aria-hidden="true">
                  <span className="dk-meter-seg" style={cssVars({ '--dk-w': `${total ? Math.min(100, (used / total) * 100).toFixed(1) : 0}%` })} />
                </span>
                <span className="dk-mono op-gpus__fig">{gbLabel(used)} / {gbLabel(total)}</span>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function MemoryShare({ models, ramTotal }) {
  const { t } = useTranslation('operate')
  const measured = models
    .filter(model => model.rss_bytes != null && model.rss_bytes > 0)
    .sort((left, right) => right.rss_bytes - left.rss_bytes)
  const modelBytes = measured.reduce((sum, model) => sum + model.rss_bytes, 0)
  const scale = ramTotal > 0 ? ramTotal : modelBytes
  const named = measured.length > LEGEND_MAX ? LEGEND_MAX - 1 : measured.length

  return (
    <div className="op-strip" aria-label={t('machine.summaryLabel')} aria-live="polite">
      <span className="dk-eyebrow">{t('machine.inMemory')}</span>
      <p className="op-strip__line op-strip__line--lead">
        <strong>{t('machine.runningCount', { count: models.length })}</strong>{' '}
        <span>
          {modelBytes > 0
            ? `${formatBytes(modelBytes)} ${t('machine.resident')}${ramTotal > 0 ? ` ${t('machine.ofRam', { total: formatBytes(ramTotal) })}` : ''}`
            : t('machine.noReadings')}
        </span>
      </p>
      <div
        className="dk-meter"
        role="img"
        aria-label={measured.map(model => `${model.model_name} ${formatBytes(model.rss_bytes)}`).join(', ') || t('machine.noneInMemory')}
      >
        {scale > 0 && measured.map((model, index) => (
          <span
            key={model.model_name}
            className={`dk-meter-seg op-share__seg op-share__seg--${index % SERIES}`}
            // A floor so a model that is small next to the host still shows up
            // as a sliver; the legend carries the size.
            style={cssVars({ '--dk-w': `${Math.max(0.8, (model.rss_bytes / scale) * 100).toFixed(2)}%` })}
            title={`${model.model_name}: ${formatBytes(model.rss_bytes)}`}
          />
        ))}
      </div>
      {measured.length > 0 && (
        <ul className="dk-meter-legend">
          {measured.slice(0, named).map((model, index) => (
            <li key={model.model_name} title={model.model_name}>
              <span className={`dk-swatch op-share__swatch op-share__swatch--${index % SERIES}`} aria-hidden="true" />
              <span className="op-share__name">{model.model_name}</span> <span className="dk-mono">{formatBytes(model.rss_bytes)}</span>
            </li>
          ))}
          {measured.length > named && <li>{t('machine.othersCount', { count: measured.length - named })}</li>}
        </ul>
      )}
    </div>
  )
}

function Fact({ label, metric, cpu = false, noDataText }) {
  const { t } = useTranslation('operate')
  const reporting = metric.reportingCount > 0
  const percent = reporting ? Math.round(metric.usagePercent) : 0
  const value = cpu
    ? `${Number(metric.busyCoreEquivalents.toFixed(1))} busy / ${metric.totalLogicalCores} cores`
    : formatCapacity(metric.used, metric.total)
  const detail = cpu
    ? `${Number(metric.idleCoreEquivalents.toFixed(1))} idle · load ${metric.load1.toFixed(2)}`
    : reporting ? `${formatCapacity(metric.available, metric.total).split(' / ')[0]} ${t('machine.available')}` : noDataText
  return (
    <li className="op-fact" aria-label={`${label} capacity`}>
      <span className="op-fact__label">{label}</span>
      <div className="dk-meter op-fact__meter" aria-hidden="true">
        {reporting && (
          <span
            className={`dk-meter-seg${percent >= 97 ? ' dk-meter-seg--error' : percent >= 90 ? ' dk-meter-seg--warn' : ''}`}
            style={cssVars({ '--dk-w': `${Math.min(100, metric.usagePercent).toFixed(1)}%` })}
          />
        )}
      </div>
      <strong className="op-fact__pct dk-mono">{reporting ? `${percent}%` : '—'}</strong>
      <div className="op-fact__text">
        <span className="op-fact__value dk-mono">{reporting ? value : t('machine.noData')}</span>
        <span className="op-fact__detail">{detail}</span>
      </div>
    </li>
  )
}

export default function HostOverview({ summary, models, ramTotal, resources }) {
  const { t } = useTranslation('operate')
  return (
    <section className="op-host" aria-label={t('machine.hostOverview')} data-testid="host-overview">
      <div className="op-host__left">
        <GpuStrip resources={resources} summary={summary} />
        <MemoryShare models={models} ramTotal={ramTotal} />
      </div>
      <div className="op-host__right">
        <span className="dk-eyebrow">{t('machine.theMachine')}</span>
        <ul className="op-facts">
          <Fact label="VRAM" metric={summary.vram} noDataText={t('machine.noGpu')} />
          <Fact label="RAM" metric={summary.ram} />
          <Fact label="CPU" metric={summary.cpu} cpu />
          <Fact label="Models disk" metric={summary.disk} />
        </ul>
      </div>
    </section>
  )
}
