import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import './tools.css'

// The drawing is made in the size it is shown at, so the text stays readable: a
// wide one on a desktop, a narrow one on a phone.
const SIZES = {
  wide: { W: 720, H: 260, PAD: { top: 16, right: 64, bottom: 36, left: 56 } },
  compact: { W: 340, H: 240, PAD: { top: 16, right: 48, bottom: 36, left: 44 } },
}

function useCompact() {
  const query = '(max-width: 640px)'
  const [compact, setCompact] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches)
  useEffect(() => {
    const mq = window.matchMedia(query)
    const on = () => setCompact(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return compact
}

function tickText(v, exp) {
  if (!Number.isFinite(v)) return ''
  if (exp) return v.toExponential(1)
  if (Math.abs(v) >= 100) return v.toFixed(0)
  if (Math.abs(v) >= 1) return v.toFixed(2)
  return v.toFixed(3)
}

// The metrics of a fine-tuning run as one thin line, with a tab for each metric.
// Loss also marks the evaluation loss as hollow dots when the run reports it.
// The end of the line carries its value, the axes are labelled, and a data table
// sits behind a disclosure for anyone who wants the numbers.
export default function LossChart({ events, totalSteps }) {
  const { t } = useTranslation('tools')
  const [metric, setMetric] = useState('loss')
  const [hover, setHover] = useState(null)
  const svgRef = useRef(null)
  const compact = useCompact()
  const { W, H, PAD } = SIZES[compact ? 'compact' : 'wide']

  const series = useMemo(() => ({
    loss: events.filter(e => e.loss > 0),
    learning_rate: events.filter(e => e.learning_rate > 0),
    grad_norm: events.filter(e => e.grad_norm > 0),
  }), [events])
  const evalPoints = useMemo(() => events.filter(e => e.eval_loss > 0), [events])
  const data = series[metric]
  const exp = metric === 'learning_rate'
  const key = metric
  const tabs = ['loss', 'learning_rate', 'grad_norm']

  const geometry = useMemo(() => {
    if (data.length < 2) return null
    const values = data.map(e => e[key]).concat(metric === 'loss' ? evalPoints.map(e => e.eval_loss) : [])
    const lo = Math.min(...values)
    const hi = Math.max(...values)
    const span = hi - lo || Math.abs(hi) || 1
    const yMin = metric === 'loss' ? Math.max(0, lo - span * 0.08) : lo - span * 0.08
    const yMax = hi + span * 0.08
    const steps = data.map(e => e.current_step)
    const xMin = Math.min(...steps)
    const xMax = Math.max(totalSteps || 0, ...steps)
    const cw = W - PAD.left - PAD.right
    const ch = H - PAD.top - PAD.bottom
    const x = (s) => PAD.left + ((s - xMin) / (xMax - xMin || 1)) * cw
    const y = (v) => PAD.top + (1 - (v - yMin) / (yMax - yMin || 1)) * ch
    const yTicks = Array.from({ length: 4 }, (_, i) => yMin + ((yMax - yMin) * i) / 3)
    const xTicks = Array.from({ length: compact ? 3 : 5 }, (_, i) => Math.round(xMin + ((xMax - xMin) * i) / (compact ? 2 : 4)))
    return { x, y, yTicks, xTicks, xMin, xMax, cw, ch }
  }, [data, evalPoints, key, metric, totalSteps, W, H, PAD, compact])

  const move = (e) => {
    if (!geometry || !svgRef.current) return
    const rect = svgRef.current.getBoundingClientRect()
    const px = ((e.clientX - rect.left) / rect.width) * W
    const step = geometry.xMin + ((px - PAD.left) / geometry.cw) * (geometry.xMax - geometry.xMin)
    let best = data[0]
    for (const d of data) if (Math.abs(d.current_step - step) < Math.abs(best.current_step - step)) best = d
    setHover(best)
  }

  const last = data[data.length - 1]
  const first = data[0]
  const readout = hover || last

  return (
    <section className="bt-chart dk-card" aria-labelledby="bt-chart-title" data-testid="job-chart">
      <header className="bt-chart__head">
        <div>
          <h2 className="bt-h2" id="bt-chart-title">{t(`chart.${metric}`)}</h2>
          <p className="dk-hint">
            {geometry
              ? t(metric === 'loss' && evalPoints.length ? 'chart.windowEval' : 'chart.window', { from: first.current_step, to: last.current_step, total: totalSteps || last.current_step })
              : t('chart.waiting')}
          </p>
        </div>
        <div className="dk-segmented" role="tablist" aria-label={t('chart.metric')}>
          {tabs.map(id => (
            <button key={id} type="button" role="tab" className="dk-seg" aria-selected={metric === id} onClick={() => { setMetric(id); setHover(null) }}>
              {t(`chart.${id}`)}
            </button>
          ))}
        </div>
      </header>
      {geometry ? (
        <>
          <svg
            ref={svgRef}
            className="bt-chart__svg"
            viewBox={`0 0 ${W} ${H}`}
            role="img"
            aria-label={t('chart.aria', { metric: t(`chart.${metric}`), last: tickText(last[key], exp), step: last.current_step })}
            onMouseMove={move}
            onMouseLeave={() => setHover(null)}
          >
            {geometry.yTicks.map((v, i) => (
              <g key={i}>
                <line className="bt-chart__grid" x1={PAD.left} x2={W - PAD.right} y1={geometry.y(v)} y2={geometry.y(v)} />
                <text className="bt-chart__tick" x={PAD.left - 8} y={geometry.y(v) + 4} textAnchor="end">{tickText(v, exp)}</text>
              </g>
            ))}
            {geometry.xTicks.map((s, i) => (
              <text key={i} className="bt-chart__tick" x={geometry.x(s)} y={H - PAD.bottom + 16} textAnchor="middle">{s}</text>
            ))}
            <text className="bt-chart__axis" x={PAD.left + geometry.cw / 2} y={H - 4} textAnchor="middle">{t('chart.step')}</text>
            <polyline className="bt-chart__line" points={data.map(e => `${geometry.x(e.current_step)},${geometry.y(e[key])}`).join(' ')} />
            {metric === 'loss' && evalPoints.map((e, i) => (
              <circle key={i} className="bt-chart__eval" cx={geometry.x(e.current_step)} cy={geometry.y(e.eval_loss)} r="4" />
            ))}
            <circle className="bt-chart__end" cx={geometry.x(last.current_step)} cy={geometry.y(last[key])} r="4" />
            <text className="bt-chart__label" x={geometry.x(last.current_step) + 10} y={geometry.y(last[key]) + 4}>{tickText(last[key], exp)}</text>
            {hover && <line className="bt-chart__cursor" x1={geometry.x(hover.current_step)} x2={geometry.x(hover.current_step)} y1={PAD.top} y2={H - PAD.bottom} />}
          </svg>
          <p className="bt-chart__readout dk-mono" aria-live="off">
            {t('chart.readout', { step: readout.current_step, value: tickText(readout[key], exp) })}
          </p>
          <details className="bt-chart__data">
            <summary>{t('chart.dataTitle')}</summary>
            <div className="dk-table-wrap bt-chart__table">
              <table className="dk-table dk-table--compact">
                <caption className="dk-sr-only">{t('chart.dataTitle')}</caption>
                <thead><tr><th>{t('chart.step')}</th><th className="dk-num">{t(`chart.${metric}`)}</th>{metric === 'loss' && <th className="dk-num">{t('chart.eval')}</th>}</tr></thead>
                <tbody>
                  {data.slice(-12).map(e => (
                    <tr key={e.current_step} data-row>
                      <td>{e.current_step}</td>
                      <td className="dk-num">{tickText(e[key], exp)}</td>
                      {metric === 'loss' && <td className="dk-num">{e.eval_loss > 0 ? tickText(e.eval_loss, false) : ''}</td>}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      ) : (
        <p className="bt-chart__empty">{t('chart.emptyText')}</p>
      )}
    </section>
  )
}
