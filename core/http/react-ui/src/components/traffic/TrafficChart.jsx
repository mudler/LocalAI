 
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { barGeometry, compactCount } from '../../utils/traffic'

// Stacked bars on a zero-based axis, drawn as SVG at the width they are shown
// at, with the kit's chart classes. Every chart carries the same four things:
//   - a text readout, filled by hover or by the arrow keys (Left, Right, Home,
//     End, Escape clears), so a value never needs a pointer;
//   - a legend whenever there are two series or more, named in text;
//   - the unit and the source in the sub line, so the reader knows what the
//     numbers are and where they come from;
//   - a data table behind a disclosure, with every value as text.
//
//   columns  [{ key, label, tick, segments: [{ id, value }] }], oldest first
//   groups   [{ id, name, series, tone }]: `series` is 1 to 6 or 'other' (the
//            kit's series tokens); `tone: 'failed'` draws the status colour,
//            which is named in the legend and the table as well.
//   format   how a value reads in the readout and the table
export default function TrafficChart({ testId, title, sub, columns, groups, format = compactCount, unit, emptyLabel }) {
  const { t } = useTranslation('traffic')
  const figureRef = useRef(null)
  const [width, setWidth] = useState(640)
  const [cursor, setCursor] = useState(null)

  useEffect(() => {
    const el = figureRef.current
    if (!el) return undefined
    const measure = () => {
      const w = Math.round(el.getBoundingClientRect().width)
      if (w > 0) setWidth(Math.max(280, Math.min(1000, w)))
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return undefined
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const geometry = useMemo(() => barGeometry(columns, {
    width,
    height: width < 520 ? 180 : 210,
    left: 44,
    right: 8,
  }), [columns, width])

  const toneOf = id => groups.find(g => g.id === id)
  const classOf = id => {
    const g = toneOf(id)
    if (g?.tone === 'failed') return 'tf-bar--failed'
    return g?.series === 'other' ? 'dk-series-other' : `dk-series-${g?.series || 1}`
  }

  const n = columns.length
  const active = cursor != null && cursor < n ? geometry.cols[cursor] : null
  const activeColumn = active ? columns[cursor] : null

  const move = (event) => {
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
    const box = event.currentTarget.getBoundingClientRect()
    const x = ((event.clientX - box.left) / box.width) * geometry.width
    let best = 0
    geometry.cols.forEach((c, i) => { if (Math.abs(c.cx - x) < Math.abs(geometry.cols[best].cx - x)) best = i })
    setCursor(best)
  }

  const readout = activeColumn
    ? [`${activeColumn.tick ?? activeColumn.label}`, ...activeColumn.segments
      .filter(seg => groups.length > 1 || seg.value > 0)
      .map(seg => `${toneOf(seg.id)?.name ?? seg.id} ${format(seg.value)}`)].join(' · ')
    : null

  const empty = geometry.max === 1 && geometry.cols.every(c => c.total === 0)

  return (
    <figure className="dk-chart tf-chart" ref={figureRef} data-testid={testId}>
      <div className="dk-chart-head">
        <h3 className="dk-chart-title">{title}</h3>
        {sub && <span className="dk-chart-sub">{sub}</span>}
      </div>
      <div className="dk-chart-readout" aria-live="polite" data-testid={testId ? `${testId}-readout` : undefined}>
        {readout
          ? <span>{readout}</span>
          : <span className="dk-chart-hint">{empty && emptyLabel ? emptyLabel : t('chart.hint')}</span>}
      </div>
      <svg
        className="dk-chart-plot"
        viewBox={`0 0 ${geometry.width} ${geometry.height}`}
        tabIndex={0}
        role="group"
        aria-roledescription={t('chart.role')}
        aria-label={`${title}${unit ? `, ${unit}` : ''}`}
        onKeyDown={move}
        onPointerMove={hover}
        onPointerLeave={() => setCursor(null)}
        onBlur={() => setCursor(null)}
      >
        {geometry.ticks.map(tick => (
          <g key={tick.value}>
            <line className={tick.value === 0 ? 'dk-chart-axis' : 'dk-chart-grid'} x1={geometry.left} x2={geometry.left + geometry.plotW} y1={tick.y} y2={tick.y} />
            <text className="dk-chart-tick dk-chart-tick--y" x={geometry.left - 8} y={tick.y + 4}>{compactCount(tick.value)}</text>
          </g>
        ))}
        {geometry.cols.map((col, i) => (
          <g key={col.key} className={cursor != null && cursor !== i ? 'dk-chart-dim' : undefined} data-mark>
            {col.segs.map(seg => (
              <rect key={seg.id} className={`dk-chart-bar dk-chart-bar--gap ${classOf(seg.id)}`} x={seg.x} y={seg.y} width={seg.w} height={seg.h} />
            ))}
            {i % geometry.every === 0 && (
              <text className="dk-chart-tick dk-chart-tick--x" x={col.cx} y={geometry.height - 6}>{columns[i].tick ?? columns[i].label}</text>
            )}
          </g>
        ))}
        {active && <line className="dk-chart-cursor" x1={active.cx} x2={active.cx} y1={geometry.top} y2={geometry.baseline} />}
      </svg>
      {groups.length > 1 && (
        <ul className="dk-chart-legend">
          {groups.map(g => (
            <li key={g.id} className={g.tone === 'failed' ? 'tf-legend--failed' : (g.series === 'other' ? 'dk-series-other' : `dk-series-${g.series}`)}>
              <span className="dk-chart-key" />
              {g.name}
            </li>
          ))}
        </ul>
      )}
      <details className="dk-chart-data">
        <summary>{t('chart.dataTable')}</summary>
        <div className="dk-table-wrap">
          <table className="dk-table dk-table--compact">
            <thead>
              <tr>
                <th scope="col">{t('chart.time')}</th>
                {groups.map(g => <th key={g.id} scope="col" className="dk-num">{g.name}</th>)}
              </tr>
            </thead>
            <tbody>
              {[...columns].reverse().map(col => (
                <tr key={col.key}>
                  <td className="dk-mono">{col.label}</td>
                  {groups.map(g => (
                    <td key={g.id} className="dk-num">{format(col.segments.find(s => s.id === g.id)?.value ?? 0)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  )
}
