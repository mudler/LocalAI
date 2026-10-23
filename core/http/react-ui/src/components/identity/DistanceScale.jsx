import { useTranslation } from 'react-i18next'
import { labelRows, pct, scaleMax, scaleTicks } from '../../utils/identity'

const at = (value, max) => ({ '--at': pct(value, max) })

// One scale for the whole answer: distance runs left to right, the cut-off is
// a line across it, and each enrolled person (or the single pair) is a dot.
// Lower means more alike. The text under it repeats the numbers, so the dots
// are never the only way to read the result.
//
//   dots     [{ id, label, distance, within, best }] one per person, any order
//   cutoff   where "same" ends
export default function DistanceScale({ dots, cutoff, noun, single = false }) {
  const { t } = useTranslation('biometrics')
  const max = scaleMax(cutoff, dots.map(d => d.distance))
  const ordered = [...dots].sort((a, b) => a.distance - b.distance)
  const rows = labelRows(ordered.map(d => (d.distance / max) * 100))
  const plot = { '--rows': Math.max(...rows, 0) }
  const summary = ordered.map(d => `${d.label} ${d.distance.toFixed(2)}`).join(', ')

  return (
    <figure className="idn-scale" data-testid="distance-scale">
      <div className="idn-scale__plot" style={plot} role="img" aria-label={t('scale.aria', { cutoff: cutoff.toFixed(2), dots: summary })}>
        <span className="idn-scale__cut" style={at(cutoff, max)}><span className="idn-scale__cut-label dk-mono">{t('scale.cutoff', { value: cutoff.toFixed(2) })}</span></span>
        <span className="idn-scale__track"><span className="idn-scale__inside" style={at(cutoff, max)} /></span>
        {ordered.map((d, i) => (
          <span key={d.id} className="idn-scale__dot" data-best={d.best ? 'true' : undefined} data-within={d.within ? 'true' : 'false'} data-row={rows[i]} style={at(d.distance, max)}>
            <span className="idn-scale__mark" />
            <span className="idn-scale__name"><strong>{d.label}</strong> <span className="dk-mono">{d.distance.toFixed(2)}</span></span>
          </span>
        ))}
        <span className="idn-scale__ticks" aria-hidden="true">
          {scaleTicks(max).map(v => <span key={v} className="idn-scale__tick dk-mono" style={at(v, max)}>{v}</span>)}
        </span>
      </div>
      <figcaption className="idn-scale__caption">
        <span>{t('scale.axis', { print: noun })}</span>
        <span>{single ? t('scale.onePair') : t('scale.oneDot', { person: noun === 'faceprint' ? t('face.person') : t('voice.speaker') })}</span>
      </figcaption>
    </figure>
  )
}
