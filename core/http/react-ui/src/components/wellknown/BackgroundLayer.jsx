import { useEffect, useMemo, useRef, useState } from 'react'
import { useAuth } from '../../context/AuthContext'
import { useWellKnown } from '../../hooks/useWellKnown'
import { filesApi } from '../../utils/api'
import { background, backgroundStyle } from '../../utils/wellKnown'
import './wellKnown.css'

// reducedMotion follows the prefers-reduced-motion setting of the system.
function useReducedMotion() {
  const query = '(prefers-reduced-motion: reduce)'
  const [reduced, setReduced] = useState(() => typeof window !== 'undefined' && !!window.matchMedia?.(query).matches)
  useEffect(() => {
    const mq = window.matchMedia?.(query)
    if (!mq) return undefined
    const on = () => setReduced(mq.matches)
    mq.addEventListener?.('change', on)
    return () => mq.removeEventListener?.('change', on)
  }, [])
  return reduced
}

// BackgroundVideo is the muted loop behind the page. It pauses while the tab
// is hidden. With reduced motion it does not play: it shows the poster, or the
// first frame that preload="metadata" loads.
function BackgroundVideo({ video, testId }) {
  const ref = useRef(null)
  const reduced = useReducedMotion()
  useEffect(() => {
    const el = ref.current
    if (!el) return undefined
    // React does not always set the muted attribute; autoplay needs it.
    el.muted = true
    const sync = () => {
      if (reduced || document.hidden) el.pause?.()
      else el.play?.()?.catch?.(() => {})
    }
    sync()
    document.addEventListener('visibilitychange', sync)
    return () => document.removeEventListener('visibilitychange', sync)
  }, [reduced, video.src])
  return (
    <video
      ref={ref}
      className={`bg-layer__video${video.fit === 'contain' ? ' bg-layer__video--contain' : ''}`}
      src={video.src}
      poster={video.poster}
      muted
      loop
      autoPlay={!reduced}
      playsInline
      preload={reduced && !video.poster ? 'metadata' : 'auto'}
      controls={false}
      disablePictureInPicture
      aria-hidden="true"
      tabIndex={-1}
      data-testid={`${testId}-video`}
    />
  )
}

// BackgroundLayer draws the background that a key of ui.json (homeBackground
// or chatBackground) names, in the box of the page that holds it and under that
// page's content. testId names the layer for tests: `${testId}`, `-art`,
// `-video` and `-scrim`. The file changes live (the change feed re-reads it),
// whether the assistant or the Appearance tab wrote it.
export default function BackgroundLayer({ bgKey, testId }) {
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const { doc } = useWellKnown('ui', { enabled: on })
  const data = doc?.data
  const style = useMemo(
    () => backgroundStyle(background(data, bgKey), (p) => filesApi.contentUrl(`/${p}`, true)),
    [data, bgKey],
  )
  if (!style) return null
  return (
    <div className="bg-layer" aria-hidden="true" data-testid={testId}>
      <div className="bg-layer__art" style={style.art} data-testid={`${testId}-art`}>
        {style.video && <BackgroundVideo video={style.video} testId={testId} />}
      </div>
      <div className="bg-layer__scrim" style={style.scrim} data-testid={`${testId}-scrim`} />
    </div>
  )
}

