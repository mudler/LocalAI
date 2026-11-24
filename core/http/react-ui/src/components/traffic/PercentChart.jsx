 
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { percentGeometry } from '../../utils/traffic'

function clock(t, seconds) {
  return new Date(t).toLocaleTimeString([], seconds
    ? { hour: '2-digit', minute: '2-digit', second: '2-digit' }
    : { hour: '2-digit', minute: '2-digit' })
}

// One percentage over the readings the page has taken, on a fixed 0 to 100
// axis. Like the memory chart it is only what was read while the page was open,
// and it says so in the sub line. Same four things as every chart here: a
// readout, the arrow keys, a text label at the end of the line and a data table.
export default function PercentChart({ testId, title, sub, samples, series = 1 }) {
  const { t } = useTranslation('traffic')
  const figureRef = useRef(null)
  const [width, setWidth] = useState(520)
  const [cursor, setCursor] = useState(null)
  const has = Array.isArray(samples) && samples.length >= 2

  useEffect(() => {
    const el = figureRef.current
    if (!has || !el) return undefined
    const measure = () => {
      const w = Math.round(el.getBoundingClientRect().width)
      if (w > 0) setWidth(Math.max(280, Math.min(920, w)))
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return undefined
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [has])

  const geometry = useMemo(() => percentGeometry(samples, { width, height: width < 480 ? 160 : 180 }), [samples, width])
  const seconds = geometry ? geometry.spanMs < 10 * 60_000 : false
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
    <figure className="dk-chart tf-chart" ref={figureRef} data-testid={testId}>
      <div className="dk-chart-head">
        <h3 className="dk-chart-title">{title}</h3>
        {sub && <span className="dk-chart-sub">{sub}</span>}
      </div>
      {geometry ? (
        <>
          <div className="dk-chart-readout" aria-live="polite">
            {active
              ? <span>{clock(active.t, true)} · <b>{Math.round(active.value)}%</b></span>
              : <span className="dk-chart-hint">{t('chart.hint')}</span>}
          </div>
          <svg
            className="dk-chart-plot"
            viewBox={`0 0 ${geometry.width} ${geometry.height}`}
            tabIndex={0}
            role="group"
            aria-roledescription={t('chart.role')}
            aria-label={`${title}. ${sub || ''}`}
            onKeyDown={move}
            onPointerMove={hover}
            onPointerLeave={() => setCursor(null)}
            onBlur={() => setCursor(null)}
          >
            {geometry.ticks.map(tick => (
              <g key={tick.value}>
                <line className={tick.value === 0 ? 'dk-chart-axis' : 'dk-chart-grid'} x1={geometry.left} x2={geometry.left + geometry.plotW} y1={tick.y} y2={tick.y} />
                <text className="dk-chart-tick dk-chart-tick--y" x={geometry.left - 8} y={tick.y + 4}>{tick.value}%</text>
              </g>
            ))}
            {geometry.xTicks.map(tick => (
              <text key={tick.t} className="dk-chart-tick dk-chart-tick--x" x={tick.x} y={geometry.height - 6}>{clock(tick.t, seconds)}</text>
            ))}
            <path className={`dk-chart-line dk-series-${series}`} d={geometry.path} />
            <circle className={`dk-chart-dot dk-chart-dot--end dk-series-${series}`} cx={geometry.end.x} cy={geometry.end.y} r="4" />
            <text className="dk-chart-label" x={geometry.end.x + 10} y={geometry.end.y + 4}>{t('chart.now', { value: `${Math.round(geometry.end.value)}%` })}</text>
            {active && (
              <>
                <line className="dk-chart-cursor" x1={active.x} x2={active.x} y1={geometry.top} y2={geometry.baseline} />
                <circle className={`dk-chart-ring dk-series-${series}`} cx={active.x} cy={active.y} r="5" />
              </>
            )}
          </svg>
          <details className="dk-chart-data">
            <summary>{t('chart.dataTable')}</summary>
            <div className="dk-table-wrap">
              <table className="dk-table dk-table--compact">
                <thead><tr><th scope="col">{t('chart.time')}</th><th scope="col" className="dk-num">{title}</th></tr></thead>
                <tbody>
                  {[...samples].reverse().map(s => (
                    <tr key={s.t}><td className="dk-mono">{clock(s.t, true)}</td><td className="dk-num">{Math.round(s.value)}%</td></tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      ) : (
        <p className="tf-note-line" data-testid={testId ? `${testId}-wait` : undefined}>{t('host.waiting')}</p>
      )}
    </figure>
  )
}
