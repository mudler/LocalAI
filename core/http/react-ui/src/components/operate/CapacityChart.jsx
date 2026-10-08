/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { chartGeometry } from '../../utils/operateStatus'
import { cssVars, gbLabel } from '../../utils/modelLedger'

function clock(t, withSeconds) {
  return new Date(t).toLocaleTimeString([], withSeconds
    ? { hour: '2-digit', minute: '2-digit', second: '2-digit' }
    : { hour: '2-digit', minute: '2-digit' })
}

// How long the samples cover, in words that match what the axis shows.
function spanWords(ms, t) {
  const minutes = Math.max(1, Math.round(ms / 60_000))
  if (minutes < 60) return t('chart.spanMinutes', { count: minutes })
  return t('chart.spanHours', { value: (ms / 3_600_000).toFixed(1) })
}

// The memory pool as it is now (one bar, solid, with the capacity as its
// edge) and, once there are two readings, as it has been since the page was
// opened. LocalAI stores no memory history, so the chart says what it is: the
// page's own readings. The axis starts at zero, the capacity is a labelled
// line, the line is labelled at its end, and a data table sits behind it.
export default function CapacityChart({ memory, samples, labelKey }) {
  const { t } = useTranslation('operate')
  const figureRef = useRef(null)
  // The plot is drawn at the width it is shown at, so its text keeps its size
  // from a phone to a wide screen instead of scaling with a fixed box.
  const [width, setWidth] = useState(640)
  const hasChart = Array.isArray(samples) && samples.length >= 2
  useEffect(() => {
    const el = figureRef.current
    if (!hasChart || !el) return undefined
    const measure = () => {
      const w = Math.round(el.getBoundingClientRect().width)
      if (w > 0) setWidth(Math.max(300, Math.min(920, w)))
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return undefined
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [hasChart])
  const geometry = useMemo(() => chartGeometry(samples, {
    width,
    height: width < 520 ? 190 : 220,
    left: 50,
    right: 92,
  }), [samples, width])
  const [cursor, setCursor] = useState(null)

  if (!memory) return null
  const pct = Math.round(memory.pct)
  const title = t(labelKey)
  const withSeconds = geometry ? geometry.spanMs < 10 * 60_000 : false
  const active = cursor != null && geometry ? geometry.points[Math.min(cursor, geometry.points.length - 1)] : null

  const move = (event) => {
    if (!geometry) return
    const n = geometry.points.length
    const at = cursor == null ? n - 1 : cursor
    if (event.key === 'ArrowLeft') setCursor(Math.max(0, at - 1))
    else if (event.key === 'ArrowRight') setCursor(Math.min(n - 1, at + 1))
    else if (event.key === 'Home') setCursor(0)
    else if (event.key === 'End') setCursor(n - 1)
    else if (event.key === 'Escape') setCursor(null)
    else return
    event.preventDefault()
  }

  const hover = (event) => {
    if (!geometry) return
    const box = event.currentTarget.getBoundingClientRect()
    const x = ((event.clientX - box.left) / box.width) * geometry.width
    let best = 0
    geometry.points.forEach((p, i) => { if (Math.abs(p.x - x) < Math.abs(geometry.points[best].x - x)) best = i })
    setCursor(best)
  }

  return (
    <section className="op-capacity" data-testid="operate-capacity" aria-label={title}>
      <header className="op-capacity__head">
        <h2 className="dk-eyebrow">{title}</h2>
        <span className="op-capacity__scope">{t('chart.scope')}</span>
      </header>

      <div className="op-capacity__now">
        <div
          className="dk-meter"
          role="img"
          aria-label={t('chart.meterLabel', { used: gbLabel(memory.used), total: gbLabel(memory.total), percent: pct })}
        >
          <span
            className={`dk-meter-seg${pct >= 97 ? ' dk-meter-seg--error' : pct >= 90 ? ' dk-meter-seg--warn' : ''}`}
            style={cssVars({ '--dk-w': `${Math.min(100, memory.pct).toFixed(1)}%` })}
          />
        </div>
        <p className="op-capacity__figure">
          <strong>{gbLabel(memory.used)}</strong> {t('chart.of', { total: gbLabel(memory.total) })}
          <span className="op-capacity__pct"> · {pct}%</span>
        </p>
      </div>

      {geometry ? (
        <figure className="dk-chart op-chart" ref={figureRef}>
          <div className="dk-chart-readout" aria-live="polite">
            {active
              ? <span>{clock(active.t, true)} · <span className="dk-mono">{gbLabel(active.used)}</span></span>
              : <span className="dk-chart-hint">{t('chart.hint', { span: spanWords(geometry.spanMs, t) })}</span>}
          </div>
          <svg
            className="dk-chart-plot"
            viewBox={`0 0 ${geometry.width} ${geometry.height}`}
            tabIndex={0}
            role="group"
            aria-roledescription="chart"
            aria-label={`${title}. ${t('chart.scope')}`}
            onKeyDown={move}
            onPointerMove={hover}
            onPointerLeave={() => setCursor(null)}
            onBlur={() => setCursor(null)}
          >
            <line className="dk-chart-grid" x1={geometry.left} x2={geometry.left + geometry.plotW} y1={geometry.midY} y2={geometry.midY} />
            <line className="dk-chart-axis" x1={geometry.left} x2={geometry.left + geometry.plotW} y1={geometry.baseline} y2={geometry.baseline} />
            <text className="dk-chart-tick dk-chart-tick--y" x={geometry.left - 8} y={geometry.baseline + 4}>0</text>
            <text className="dk-chart-tick dk-chart-tick--y" x={geometry.left - 8} y={geometry.midY + 4}>{t('chart.gbTick', { value: Math.round(geometry.mid / 1024 ** 3) })}</text>
            <line className="dk-chart-threshold" x1={geometry.left} x2={geometry.left + geometry.plotW} y1={geometry.capacityY} y2={geometry.capacityY} />
            <text className="dk-chart-threshold-label" x={geometry.left + 4} y={geometry.capacityY - 6}>
              {t('chart.capacity', { value: gbLabel(geometry.capacity) })}
            </text>
            {geometry.xTicks.map(tick => (
              <text key={tick.t} className="dk-chart-tick dk-chart-tick--x" x={tick.x} y={geometry.height - 6}>{clock(tick.t, withSeconds)}</text>
            ))}
            <path className="dk-chart-line dk-series-1" d={geometry.path} />
            <circle className="dk-chart-dot dk-chart-dot--end dk-series-1" cx={geometry.end.x} cy={geometry.end.y} r="4" />
            <text className="dk-chart-label" x={geometry.end.x + 10} y={geometry.end.y + 4}>
              {t('chart.now', { value: gbLabel(geometry.end.used) })}
            </text>
            {active && (
              <>
                <line className="dk-chart-cursor" x1={active.x} x2={active.x} y1={geometry.top} y2={geometry.baseline} />
                <circle className="dk-chart-ring dk-series-1" cx={active.x} cy={active.y} r="5" />
              </>
            )}
          </svg>
          <details className="dk-chart-data">
            <summary>{t('chart.dataTable')}</summary>
            <div className="dk-table-wrap">
              <table className="dk-table dk-table--compact">
                <thead>
                  <tr>
                    <th scope="col">{t('chart.time')}</th>
                    <th scope="col" className="dk-num">{t('chart.inUse')}</th>
                    <th scope="col" className="dk-num">{t('chart.capacityColumn')}</th>
                  </tr>
                </thead>
                <tbody>
                  {[...samples].reverse().map(sample => (
                    <tr key={sample.t}>
                      <td className="dk-mono">{clock(sample.t, true)}</td>
                      <td className="dk-num dk-mono">{gbLabel(sample.used)}</td>
                      <td className="dk-num dk-mono">{gbLabel(sample.total)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </figure>
      ) : (
        <p className="op-capacity__wait" data-testid="operate-capacity-wait">{t('chart.waiting')}</p>
      )}
    </section>
  )
}
