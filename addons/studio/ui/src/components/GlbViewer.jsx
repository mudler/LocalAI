import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { parseGlb } from '../utils/glb'
import Icon from './Icon'

/* ── WebGL2 GLB viewer ──────────────────────────────────────────────────────
 * Ported from the trellis2cpp demo server's hand-rolled viewer
 * (localai-org/trellis2cpp server/web/index.html): quaternion trackball,
 * metallic-roughness PBR with a procedural environment, ACES tonemapping,
 * hidden-line wireframe. Kept dependency-free — the renderer is tuned for
 * the dense unoriented dual-grid meshes TRELLIS.2 produces, which generic
 * GLTF viewers shade poorly.
 *
 * Deltas from the demo: input is a parsed GLB (see utils/glb.js) instead of
 * the demo's T2MESH stream; COLOR_0 arrives LINEAR (no gamma decode — the
 * demo's pow(2.2) would double-darken it); vertex attributes upload as
 * normalized integers straight from the GLB buffers; an optional UV-texture
 * path covers the atlas-baked form; the base orientation drops the demo's
 * Z-up -> Y-up turn because baked GLBs are already Y-up.
 */

import { createGlbViewer } from "../../../../internal/glb-viewer/renderer.js"
export { createGlbViewer } from "../../../../internal/glb-viewer/renderer.js"

export default function GlbViewer({ blob }) {
  const { t } = useTranslation('media')
  const canvasRef = useRef(null)
  const viewerRef = useRef(null)
  const [glError, setGlError] = useState(null) // 'no-webgl2' | 'context-lost' | parse error text
  const [stats, setStats] = useState(null)
  const [wire, setWire] = useState(false)
  const [spin, setSpin] = useState(true)

  useEffect(() => {   // GL lifecycle — once per mount
    let viewer = null
    try {
      viewer = createGlbViewer(canvasRef.current, { onContextLost: () => setGlError('context-lost') })
    } catch {
      viewer = null
    }
    if (!viewer) {
      setGlError('no-webgl2')
      return undefined
    }
    viewer.onSpinChanged(setSpin)
    viewerRef.current = viewer
    return () => {
      viewerRef.current = null
      viewer.dispose()
    }
  }, [])

  useEffect(() => {   // (re)load when the blob changes
    if (!blob) {
      viewerRef.current?.clear()
      setStats(null)
      return undefined
    }
    let cancelled = false
    ;(async () => {
      try {
        // Parse before touching GL: stats and parse errors surface even when
        // WebGL2 is unavailable (e.g. headless CI), and the download button
        // keeps working either way.
        const mesh = parseGlb(await blob.arrayBuffer())
        if (cancelled) return
        setStats({
          nVerts: mesh.nVerts,
          nTris: mesh.nTris,
          pbr: !!(mesh.color0 || mesh.baseColorPng),
        })
        if (viewerRef.current) await viewerRef.current.setMesh(mesh)
      } catch (err) {
        if (!cancelled) setGlError(err.message)
      }
    })()
    return () => { cancelled = true }
  }, [blob])

  const toggleWire = () => {
    const next = !wire
    setWire(next)
    viewerRef.current?.setWire(next)
  }
  const toggleSpin = () => {
    const next = !spin
    setSpin(next)
    viewerRef.current?.setSpin(next)
  }

  return (
    <div className="glb-viewer" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)', width: '100%' }}>
      <canvas
        ref={canvasRef}
        style={{ width: '100%', aspectRatio: '4 / 3', borderRadius: 'var(--radius-md)', background: '#101418', touchAction: 'none' }}
        data-testid="glb-canvas"
      />
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
        <button type="button" className={`btn btn-sm ${wire ? 'btn-primary' : 'btn-secondary'}`} onClick={toggleWire}>
          <Icon name="square-dashed" /> {t('threed.viewer.wireframe')}
        </button>
        <button type="button" className={`btn btn-sm ${spin ? 'btn-primary' : 'btn-secondary'}`} onClick={toggleSpin}>
          <Icon name="refresh" /> {t('threed.viewer.autoRotate')}
        </button>
        {stats && (
          <span style={{ color: 'var(--color-text-muted)', fontSize: '0.85em' }} data-testid="glb-stats">
            {t('threed.viewer.stats', { verts: stats.nVerts.toLocaleString(), tris: stats.nTris.toLocaleString() })}
            {stats.pbr ? ' · PBR' : ''}
          </span>
        )}
      </div>
      {glError === 'no-webgl2' && <p style={{ color: 'var(--color-text-muted)' }}>{t('threed.viewer.noWebgl')}</p>}
      {glError === 'context-lost' && <p style={{ color: 'var(--color-text-muted)' }}>{t('threed.viewer.contextLost')}</p>}
      {glError && glError !== 'no-webgl2' && glError !== 'context-lost' && (
        <p style={{ color: 'var(--color-error)' }}>{glError}</p>
      )}
      <p style={{ color: 'var(--color-text-muted)', fontSize: '0.8em', margin: 0 }}>{t('threed.viewer.hint')}</p>
    </div>
  )
}
