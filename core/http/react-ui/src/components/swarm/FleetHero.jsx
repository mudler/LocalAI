/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { formatBytes } from '../nodes/nodeStatus'
import { cssVars } from '../../utils/modelLedger'

function Gauge({ label, reading, t }) {
  const known = reading.total > 0
  const percent = known ? Math.round(reading.usagePercent) : 0
  const level = percent >= 90 ? ' dk-meter-seg--error' : percent >= 75 ? ' dk-meter-seg--warn' : ''
  return (
    <div className="sw-hero__gauge">
      <span className="sw-hero__kick">{label}</span>
      <strong className="sw-hero__big">{known ? `${formatBytes(reading.used)} / ${formatBytes(reading.total)}` : t('table.noReading')}</strong>
      {known && (
        <span className="dk-meter sw-hero__meter" role="img" aria-label={t('table.memoryAria', { used: formatBytes(reading.used), total: formatBytes(reading.total), percent })}>
          <span className={`dk-meter-seg${level}`} style={cssVars({ '--dk-w': `${percent}%` })} />
        </span>
      )}
    </div>
  )
}

// The fleet in one card: how many nodes are healthy, how full the GPU and
// system memory are across them, how many models run, and how many nodes need
// a look. `summary` is summarizeFleet(nodes).
export default function FleetHero({ summary, running, attention }) {
  const { t } = useTranslation('swarm')
  const { total, healthy } = summary.health
  const share = total > 0 ? Math.round((healthy / total) * 100) : 0
  const tone = total === 0 ? 'idle' : healthy === total ? 'ok' : share < 50 ? 'error' : 'warn'
  return (
    <section className="dk-card sw-hero" aria-label={t('hero.label')} data-testid="fleet-hero">
      <div className="sw-hero__ring">
        <svg viewBox="0 0 36 36" className="sw-hero__svg" role="img" aria-label={t('hero.ringAria', { healthy, total })}>
          <circle className="sw-hero__track" cx="18" cy="18" r="15.5" />
          <circle className="sw-hero__arc" data-tone={tone} cx="18" cy="18" r="15.5" pathLength="100" strokeDasharray={`${share} 100`} />
        </svg>
        <div>
          <strong className="sw-hero__big">{t('hero.healthy', { healthy, total })}</strong>
          <span className="sw-hero__sub">{t('hero.healthyLabel', { count: total })}</span>
        </div>
      </div>
      <Gauge label={t('hero.vram')} reading={summary.vram} t={t} />
      <Gauge label={t('hero.ram')} reading={summary.ram} t={t} />
      <div className="sw-hero__gauge">
        <span className="sw-hero__kick">{t('hero.models')}</span>
        <strong className="sw-hero__big">{running}</strong>
        {attention > 0
          ? <span className="sw-pill" data-level="warn">{t('hero.attention', { count: attention })}</span>
          : <span className="sw-hero__sub">{t('hero.allClear')}</span>}
      </div>
    </section>
  )
}
