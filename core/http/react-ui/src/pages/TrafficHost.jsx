/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useResources } from '../hooks/useResources'
import { useOperateSummary } from '../contexts/OperateSummaryContext'
import { appendSample, capacityOf } from '../utils/operateStatus'
import { appendReading, cpuReading, gpuRows } from '../utils/traffic'
import { capacityReading } from '../utils/nodeFleet'
import { formatBytes } from '../utils/format'
import { cssVars } from '../utils/modelLedger'
import { nodeState, isDown } from '../utils/swarm'
import CapacityChart from '../components/operate/CapacityChart'
import PercentChart from '../components/traffic/PercentChart'
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import './traffic.css'

function Meter({ label, used, total, tail }) {
  const pct = total > 0 ? Math.min(100, (used / total) * 100) : 0
  const level = pct >= 97 ? ' dk-meter-seg--error' : pct >= 90 ? ' dk-meter-seg--warn' : ''
  return (
    <div className="tf-meter">
      <span className="tf-meter__label">{label}</span>
      <span className="dk-meter tf-meter__bar" role="img" aria-label={`${label}: ${formatBytes(used)} / ${formatBytes(total)}, ${Math.round(pct)}%`}>
        <span className={`dk-meter-seg${level}`} style={cssVars({ '--dk-w': `${pct.toFixed(1)}%` })} />
      </span>
      <span className="dk-mono tf-meter__fig">{formatBytes(used)} / {formatBytes(total)}{tail}</span>
    </div>
  )
}

function ReadingCell({ total, available }) {
  const { t } = useTranslation('traffic')
  const reading = capacityReading(total, available)
  if (!reading) return <span className="tf-sub">{t('host.noReading')}</span>
  return (
    <span className="tf-cell-meter">
      <span className="dk-meter" role="img" aria-label={`${Math.round(reading.usagePercent)}%`}>
        <span className={`dk-meter-seg${reading.usagePercent >= 90 ? ' dk-meter-seg--warn' : ''}`} style={cssVars({ '--dk-w': `${reading.usagePercent.toFixed(1)}%` })} />
      </span>
      <span className="dk-mono">{formatBytes(reading.used)} / {formatBytes(reading.total)}</span>
    </span>
  )
}

// The machine as it is now, and, for a cluster, every node. What it can draw is
// what LocalAI reports: memory per GPU, system memory, the CPU share, and the
// models disk. It does not report GPU utilisation or temperature, so neither is
// drawn. The two charts are the page's own readings, taken every 5 seconds
// while it is open and kept in the browser (at most 240, about 20 minutes).
export default function TrafficHost() {
  const { t } = useTranslation('traffic')
  const { resources, loading, error } = useResources(5000)
  const summary = useOperateSummary()
  const distributed = summary?.distributed === true
  const nodes = summary?.nodes || []
  const [samples, setSamples] = useState({ memory: [], cpu: [] })

  // One reading per answer from the resources poll.
  useEffect(() => {
    if (!resources) return
    const now = Date.now()
    setSamples(prev => ({
      memory: appendSample(prev.memory, capacityOf({ resources, nodes, distributed }), now),
      cpu: appendReading(prev.cpu, cpuReading(resources), now),
    }))
    // `nodes` changes on its own poll; a reading is taken per resources answer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resources])

  const memory = capacityOf({ resources, nodes, distributed })
  const gpus = gpuRows(resources)
  const ram = resources?.ram
  const disk = resources?.disk
  const cpu = resources?.cpu
  const label = memory?.kind === 'gpu' ? 'chart.titleGpu' : memory?.kind === 'ram' ? 'chart.titleRam' : memory?.kind === 'cluster-gpu' ? 'chart.titleClusterGpu' : 'chart.titleClusterRam'

  return (
    <div className="page page--wide tf-page" data-testid="traffic-host">
      <header className="tf-head">
        <div className="tf-head__lead">
          <h1 className="tf-title">{t('host.title')}</h1>
        </div>
      </header>
      <p className="tf-source">{t('host.sources')}</p>

      {loading && !resources ? (
        <div className="tf-loading"><LoadingSpinner size="lg" /></div>
      ) : !resources ? (
        <div className="tf-error" role="alert" data-testid="host-error">
          <Icon name="alert-circle" />
          <span>{t('host.error', { message: String(error || '') })}</span>
        </div>
      ) : (
        <>
          <section className="tf-section" data-testid="host-snapshot">
            <h2 className="tf-h2">{distributed ? t('host.controller') : t('host.now')} <span className="tf-sub">{t('host.nowSub')}</span></h2>
            <div className="tf-meters">
              {gpus.length === 0 && <p className="tf-note-line">{t('host.noGpu')}</p>}
              {gpus.map(g => (
                <Meter key={g.id} label={g.name} used={g.used} total={g.total} tail={g.pct != null ? ` · ${Math.round(g.pct)}%` : ''} />
              ))}
              {ram?.total > 0 && <Meter label={t('host.systemMemory')} used={Math.max(0, ram.total - (ram.available ?? ram.free ?? 0))} total={ram.total} />}
              {disk?.total > 0 && <Meter label={t('host.modelsDisk')} used={Math.max(0, disk.total - (disk.available ?? 0))} total={disk.total} />}
              {cpu?.logical_cores > 0 && (
                <div className="tf-meter">
                  <span className="tf-meter__label">{t('host.cpu')}</span>
                  <span className="dk-meter tf-meter__bar" role="img" aria-label={`${t('host.cpu')}: ${Math.round(cpu.usage_percent)}%`}>
                    <span className="dk-meter-seg" style={cssVars({ '--dk-w': `${Math.max(0, Math.min(100, cpu.usage_percent)).toFixed(1)}%` })} />
                  </span>
                  <span className="dk-mono tf-meter__fig">{Math.round(cpu.usage_percent)}% · {t('host.cores', { count: cpu.logical_cores })}</span>
                </div>
              )}
            </div>
            <p className="tf-note-line" data-testid="host-not-reported">{t('host.notReported')}</p>
          </section>

          <section className="tf-section" data-testid="host-history">
            <h2 className="tf-h2">{t('host.history')} <span className="tf-badge" data-testid="since-open">{t('host.sinceOpen')}</span></h2>
            {memory
              ? <CapacityChart memory={memory} samples={samples.memory} labelKey={label} scopeKey="chart.scopeHost" waitingKey="chart.waitingHost" />
              : <p className="tf-note-line">{t('host.noMemory')}</p>}
            {!distributed && cpu?.logical_cores > 0 && (
              <PercentChart testId="cpu-chart" title={t('host.cpu')} sub={t('host.cpuSub')} samples={samples.cpu} series={2} />
            )}
          </section>
        </>
      )}

      {distributed && (
        <section className="tf-section" data-testid="host-nodes">
          <div className="tf-section__head">
            <h2 className="tf-h2">{t('host.nodes')} <span className="tf-sub">{t('host.nodesSub', { count: nodes.length })}</span></h2>
            <Link className="dk-link" to="/app/nodes">{t('host.allNodes')}</Link>
          </div>
          {nodes.length === 0 ? (
            <p className="tf-note-line">{t('host.noNodes')}</p>
          ) : (
            <div className="dk-table-wrap">
              <table className="dk-table tf-table">
                <caption className="dk-sr-only">{t('host.nodes')}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t('host.node')}</th>
                    <th scope="col">{t('host.state')}</th>
                    <th scope="col">{t('host.gpuMemory')}</th>
                    <th scope="col" className="dk-hide-phone">{t('host.systemMemory')}</th>
                    <th scope="col" className="dk-num dk-hide-phone">{t('host.cpu')}</th>
                  </tr>
                </thead>
                <tbody>
                  {nodes.map(n => {
                    const state = nodeState(n)
                    return (
                      <tr key={n.id} data-row data-entity={n.name} data-down={isDown(n) ? '' : undefined}>
                        <td><Link className="dk-table-name dk-mono tf-name" to={`/app/nodes/${encodeURIComponent(n.id)}`}>{n.name || n.id}</Link></td>
                        <td><span className="tf-state" data-level={state.level}>{t(`host.states.${state.key}`)}</span></td>
                        <td><ReadingCell total={n.total_vram} available={n.available_vram} /></td>
                        <td className="dk-hide-phone"><ReadingCell total={n.total_ram} available={n.available_ram} /></td>
                        <td className="dk-num dk-hide-phone">{typeof n.cpu_usage_percent === 'number' ? `${Math.round(n.cpu_usage_percent)}%` : '-'}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
          <p className="tf-note-line">{t('host.lostNodes')}</p>
        </section>
      )}
    </div>
  )
}
