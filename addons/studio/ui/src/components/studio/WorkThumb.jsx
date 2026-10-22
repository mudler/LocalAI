import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { aspectOf, TYPE_ICON, TYPE_INFO, waveBars } from '../../utils/studioWork'
import { cssVars } from '../../utils/modelLedger'

// What a result looks like on a tile or a node.
//
//   images       the picture the server kept
//   video        its first frame, read from the file
//   3D           the small picture it was made from, else a mesh placeholder
//   audio        a drawn waveform. It is a placeholder that is the same for the
//                same result every time; it is not the sound
//   diarization  one bar per speaker the run found
//
// A picture whose file is gone from the server (the output folder was cleaned)
// falls back to the type's icon rather than a broken image.
export default function WorkThumb({ item, compact = false }) {
  const { t } = useTranslation('media')
  const [failed, setFailed] = useState(false)
  const kind = TYPE_INFO[item.type].kind
  const ratio = compact ? undefined : Math.min(1.8, Math.max(0.55, 1 / aspectOf(item)))
  const frame = ratio ? cssVars({ '--studio-ratio': ratio }) : undefined

  let body
  if (kind === 'image' && item.url && !failed) {
    body = <img src={item.url} alt="" loading="lazy" onError={() => setFailed(true)} />
  } else if (kind === 'video' && item.url && !failed) {
    // #t=0.1 asks for a frame past the black first one; preload=metadata keeps it to one small range request.
    body = <video src={`${item.url}#t=0.1`} preload="metadata" muted playsInline tabIndex={-1} aria-hidden="true" onError={() => setFailed(true)} />
  } else if (kind === 'mesh' && item.thumb) {
    body = <img src={item.thumb} alt="" loading="lazy" />
  } else if (kind === 'mesh') {
    body = <Icon name="cube" className="studio-thumb__glyph" />
  } else if (kind === 'audio') {
    body = (
      <span className="studio-wave" aria-hidden="true">
        {waveBars(item.id, compact ? 28 : 44).map((h, i) => <i key={i} style={cssVars({ '--h': h })} />)}
      </span>
    )
  } else if (kind === 'speakers') {
    const n = Math.max(1, Math.min(6, Number(item.params?.speakers) || 1))
    body = (
      <span className="studio-lanes" aria-hidden="true">
        {Array.from({ length: n }, (_, i) => <i key={i} data-speaker={i % 4} style={cssVars({ '--from': `${(i * 17) % 40}%`, '--to': `${60 + ((i * 23) % 40)}%` })} />)}
      </span>
    )
  } else {
    body = <Icon name={TYPE_ICON[item.type]} className="studio-thumb__glyph" title={failed ? t('studio.work.fileGone') : undefined} />
  }

  return (
    <span className={`studio-thumb studio-thumb--${kind}${compact ? ' studio-thumb--compact' : ''}`} style={frame} data-failed={failed || undefined}>
      {body}
    </span>
  )
}
