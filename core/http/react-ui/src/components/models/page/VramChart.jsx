import { gbLabel } from '../../../utils/modelLedger'
import { contextLabel } from '../../../utils/placement'
import './VramChart.css'

const W = 640
const H = 230
const LEFT = 8
const RIGHT = 8
const TOP = 34
const BOTTOM = 26

// Memory by context length: one solid bar per length, from zero, with the limit
// drawn as a labelled dashed line. Bars over the limit change colour and the
// words next to the chart say the same thing, so colour never carries it alone.
// A data table sits behind the chart, as the kit asks of every chart.
//
//   points     [{ ctx, bytes }]
//   limit      bytes, the most this machine lets a model use
//   selected   the context the rest of the page is looking at
//   poolLabel  names the limit line ("22.8 GB GPU memory")
export default function VramChart({ points, limit, selected, onPick, title, poolLabel, t }) {
  if (points.length < 2) return null
  const top = Math.max(limit, ...points.map(p => p.bytes)) * 1.08
  const plotW = W - LEFT - RIGHT
  const plotH = H - TOP - BOTTOM
  const slot = plotW / points.length
  const barW = Math.min(64, slot * 0.7)
  const y = bytes => TOP + plotH - (bytes / top) * plotH
  const limitY = y(limit)
  return (
    <figure className="dk-chart vramchart" data-testid="vram-chart">
      <div className="dk-chart-head">
        <h3 className="dk-chart-title">{title}</h3>
      </div>
      <svg
        className="dk-chart-plot"
        viewBox={`0 0 ${W} ${H}`}
        role="group"
        aria-roledescription="chart"
        aria-label={title}
      >
        <line className="dk-chart-axis" x1={LEFT} x2={W - RIGHT} y1={TOP + plotH} y2={TOP + plotH} />
        {points.map((p, i) => {
          const x = LEFT + slot * i + (slot - barW) / 2
          const over = p.bytes > limit
          const on = p.ctx === selected
          const barY = y(p.bytes)
          return (
            <g
              key={p.ctx}
              className="vramchart__col"
              data-over={over ? 'true' : 'false'}
              data-selected={on ? 'true' : 'false'}
              data-testid={`vram-bar-${p.ctx}`}
              onClick={() => onPick(p.ctx)}
            >
              <rect className="vramchart__hit" x={LEFT + slot * i} y={TOP} width={slot} height={plotH} />
              <rect className="dk-chart-bar vramchart__bar" x={x} y={barY} width={barW} height={TOP + plotH - barY} />
              <text className="dk-chart-value vramchart__value" x={x + barW / 2} y={14} textAnchor="middle">{(p.bytes / 1024 ** 3).toFixed(1)}</text>
              <text className="dk-chart-tick dk-chart-tick--x vramchart__tick" x={x + barW / 2} y={H - 8}>{contextLabel(p.ctx)}</text>
            </g>
          )
        })}
        <line className="dk-chart-threshold" x1={LEFT} x2={W - RIGHT} y1={limitY} y2={limitY} />
        <text className="dk-chart-threshold-label vramchart__limit-label" x={LEFT + 2} y={limitY - 6}>{poolLabel}</text>
      </svg>
      <details className="dk-chart-data">
        <summary>{t('page.fit.dataTable')}</summary>
        <div className="dk-table-wrap">
          <table className="dk-table dk-table--compact">
            <thead>
              <tr>
                <th scope="col">{t('page.fit.context')}</th>
                <th scope="col" className="dk-num">{t('page.fit.needs')}</th>
                <th scope="col">{t('page.fit.verdict')}</th>
              </tr>
            </thead>
            <tbody>
              {points.map(p => (
                <tr key={p.ctx}>
                  <td className="dk-mono">{contextLabel(p.ctx)}</td>
                  <td className="dk-num dk-mono">{gbLabel(p.bytes)}</td>
                  <td>{p.bytes > limit ? t('page.fit.tableOver') : t('page.fit.tableFits')}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  )
}
