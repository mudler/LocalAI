import { useTranslation } from 'react-i18next'
import { axisTicks, clock } from '../../utils/diarization'
import { cssVars } from '../../utils/modelLedger'

// Who spoke when: one lane per speaker, a bar for each stretch of speech, and a
// time axis. Drawn from the run's own segments; nothing here is estimated.
// The segment list under it is the same data as text.
//
//   rows     speakerRows(result)
//   length   the run's length in seconds
export default function Timeline({ rows, length }) {
  const { t } = useTranslation('media')
  const ticks = axisTicks(length)
  return (
    <div className="ws-timeline" role="img" aria-label={t('studio.workspace.diarization.timeline', { count: rows.length, length: clock(length) })} data-testid="ws-timeline">
      <div className="ws-timeline__axis" aria-hidden="true">
        {ticks.map(tick => <span key={tick.at} style={cssVars({ '--at': `${tick.pct}%` })}>{clock(tick.at)}</span>)}
      </div>
      {rows.map(row => (
        <div key={row.label} className="ws-timeline__lane" data-speaker={row.index % 6} aria-hidden="true">
          <span className="ws-timeline__who"><i />{row.name || row.id}</span>
          <span className="ws-timeline__track">
            {row.segments.map((seg, i) => (
              <b key={seg.id ?? i} style={cssVars({ '--from': `${(Number(seg.start) / length) * 100}%`, '--len': `${Math.max(0.4, ((Number(seg.end) - Number(seg.start)) / length) * 100)}%` })} />
            ))}
          </span>
        </div>
      ))}
    </div>
  )
}
