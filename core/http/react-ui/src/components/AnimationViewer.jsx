// SPDX-License-Identifier: MIT
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { animationPose, parseAnimationGlb } from '../utils/animationGlb'

export default function AnimationViewer({ blob }) {
  const { t } = useTranslation('media')
  const canvas = useRef(null)
  const drag = useRef(null)
  const [animation, setAnimation] = useState(null)
  const [error, setError] = useState(null)
  const [playing, setPlaying] = useState(true)
  const [time, setTime] = useState(0)
  const [yaw, setYaw] = useState(-0.6)
  const [zoom, setZoom] = useState(1)
  const [size, setSize] = useState([640, 480])

  useEffect(() => {
    let cancelled = false
    setAnimation(null)
    setError(null)
    setTime(0)
    blob.arrayBuffer().then(parseAnimationGlb).then(value => {
      animationPose(value, 0)
      if (!cancelled) { setAnimation(value); setPlaying(true) }
    }).catch(err => { if (!cancelled) setError(err.message) })
    return () => { cancelled = true }
  }, [blob])

  useEffect(() => {
    if (!playing || !animation?.duration) return
    let frame, previous
    const tick = now => {
      if (previous === undefined) previous = now
      if (now - previous >= 1000 / 30) {
        // Capture elapsed time before React defers the update; dropped frames
        // must skip ahead on the timeline rather than slow down playback.
        const elapsed = (now - previous) / 1000
        setTime(value => (value + elapsed) % animation.duration)
        previous = now
      }
      frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [playing, animation])

  useEffect(() => {
    if (!canvas.current) return
    const observer = new ResizeObserver(([entry]) => setSize([Math.round(entry.contentRect.width), Math.round(entry.contentRect.height)]))
    observer.observe(canvas.current)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    if (!canvas.current) return
    const context = canvas.current.getContext('2d')
    if (!context) return
    context.clearRect(0, 0, ...size)
    if (!animation) return
    try {
      const positions = animationPose(animation, time)
      const [width, height] = size
      const root = positions[animation.order[0]]
      const scale = Math.min(width, height) * 0.32 * zoom
      const project = ([x, y, z]) => {
        x -= root[0]; z -= root[2]
        return [width/2 + (x*Math.cos(yaw)-z*Math.sin(yaw))*scale,
          height*0.8 - (y + (x*Math.sin(yaw)+z*Math.cos(yaw))*0.2)*scale]
      }
      const styles = getComputedStyle(canvas.current)
      context.clearRect(0, 0, width, height)
      context.strokeStyle = styles.getPropertyValue('--color-border').trim() || '#465566'
      context.lineWidth = 1
      for (let i = -5; i <= 5; i++) {
        for (const ends of [[[root[0]-5, 0, root[2]+i], [root[0]+5, 0, root[2]+i]], [[root[0]+i, 0, root[2]-5], [root[0]+i, 0, root[2]+5]]]) {
          const a = project(ends[0]), b = project(ends[1])
          context.beginPath(); context.moveTo(...a); context.lineTo(...b); context.stroke()
        }
      }
      context.strokeStyle = styles.getPropertyValue('--color-primary').trim() || '#438eff'
      context.fillStyle = context.strokeStyle
      context.lineWidth = 3
      positions.forEach((position, index) => {
        const point = project(position)
        const parent = animation.nodes[index].parent
        if (parent >= 0) {
          context.beginPath(); context.moveTo(...project(positions[parent])); context.lineTo(...point); context.stroke()
        }
        context.beginPath(); context.arc(...point, 3, 0, 2*Math.PI); context.fill()
      })
    } catch (err) { setError(err.message); setPlaying(false) }
  }, [animation, time, yaw, zoom, size])

  return (
    <div className="stack animation-viewer" data-testid="animation-viewer">
      {error && <p role="alert" className="form-error">{error}</p>}
      <canvas
        ref={canvas} width={size[0]} height={size[1]}
        aria-label={t('threed.animation.preview', 'Animated skeleton preview')}
        onPointerDown={event => { drag.current = event.clientX; event.currentTarget.setPointerCapture(event.pointerId) }}
        onPointerMove={event => { if (drag.current !== null) { setYaw(value => value + (event.clientX-drag.current)*0.01); drag.current = event.clientX } }}
        onPointerUp={() => { drag.current = null }}
        onPointerCancel={() => { drag.current = null }}
      />
      {animation && <div className="stack">
        <div className="hstack">
          <button type="button" className="btn btn-secondary" onClick={() => setPlaying(!playing)}>
            {playing ? t('threed.animation.pause', 'Pause') : t('threed.animation.play', 'Play')}
          </button>
          <button type="button" className="btn btn-secondary" onClick={() => { setYaw(-0.6); setZoom(1) }}>{t('threed.animation.reset', 'Reset view')}</button>
          <output data-testid="animation-time">{time.toFixed(2)} / {animation.duration.toFixed(2)} s</output>
        </div>
        <label className="form-label" htmlFor="animation-timeline">{t('threed.animation.timeline', 'Timeline')}</label>
        <input id="animation-timeline" type="range" min="0" max={animation.duration} step="0.01" value={time} onChange={event => { setPlaying(false); setTime(Number(event.target.value)) }} />
        <label className="form-label" htmlFor="animation-zoom">{t('threed.animation.zoom', 'Zoom')}</label>
        <input id="animation-zoom" type="range" min="0.3" max="3" step="0.05" value={zoom} onChange={event => setZoom(Number(event.target.value))} />
      </div>}
    </div>
  )
}
