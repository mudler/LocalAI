/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import Skeleton from '../Skeleton'
import Button from '../ui/Button'
import TableView from './TableView'
import { filesApi } from '../../utils/api'
import { loadOfficeBytes } from '../../utils/officePreview'
import { docxFrameSource } from '../../utils/officeDocxFrame'

// A Word, Excel or PowerPoint file drawn in the browser from its own bytes.
// The libraries that read the formats load on first use (dynamic import), so
// the main bundle does not carry them, and none of them uses the network.
//
//   docx  an isolated frame (no scripts, a policy that loads nothing) that
//         holds the pages as the document lays them out
//   xlsx  the table view: sheet tabs, a sticky header, capped rows
//   pptx  one slide at a time on a canvas, Previous and Next
//
// Anything that goes wrong (the read, a file the library cannot open, a file
// over the size cap) calls onFallback(reason), and the pane shows the
// server's text view instead. Download is always in the bar above the file.
export default function OfficeView({ item, kind, t, onFallback }) {
  const [state, setState] = useState({ buf: null, ready: false })
  const [full, setFull] = useState(false)
  const fallback = useRef(onFallback)
  fallback.current = onFallback
  const fail = useCallback((reason, error) => fallback.current?.(reason, error), [])
  const ready = useCallback(() => setState(s => (s.ready ? s : { ...s, ready: true })), [])

  useEffect(() => {
    setState({ buf: null, ready: false })
    const ctl = new AbortController()
    loadOfficeBytes(filesApi.contentUrl(item.path), { signal: ctl.signal }).then((out) => {
      if (ctl.signal.aborted) return
      if (out.buf) setState({ buf: out.buf, ready: false })
      else fail(out.reason, out.error)
    })
    return () => ctl.abort()
  }, [item.path, item.size, kind, fail])

  const common = { item, t, buf: state.buf, onReady: ready, onFail: fail, full }
  return (
    <div className="pv-office" data-testid="preview-office" data-office={kind}>
      <div className="pv-office__bar">
        {kind === 'docx' && state.ready && (
          <Button size="sm" variant="ghost" icon={full ? 'minimize' : 'maximize'} aria-pressed={full} onClick={() => setFull(f => !f)} data-testid="preview-docx-zoom">
            {full ? t('preview.zoomOut') : t('preview.zoomIn')}
          </Button>
        )}
        <Button size="sm" icon="download" href={filesApi.contentUrl(item.path)} download={item.name} data-testid="preview-office-download">{t('download')}</Button>
      </div>
      {!state.ready && (
        <div className="pv pv--loading" role="status" aria-busy="true" aria-label={t('preview.loading', { name: item.name })} data-testid="preview-loading">
          <Skeleton variant="block" className="pv__sk-table" />
        </div>
      )}
      <div className={state.ready ? 'pv-office__stage' : 'pv-office__stage pv-office__stage--wait'}>
        {state.buf && kind === 'docx' && <DocxFrame {...common} />}
        {state.buf && kind === 'xlsx' && <SheetGrid {...common} />}
        {state.buf && kind === 'pptx' && <SlideDeck {...common} />}
      </div>
    </div>
  )
}

// The colours of the pane, read once, for the frame's own page.
function paneColors() {
  const s = getComputedStyle(document.documentElement)
  const pick = (name, fallback) => s.getPropertyValue(name).trim() || fallback
  return { bg: pick('--dk-inset', 'Canvas'), fg: pick('--dk-text', 'CanvasText'), edge: pick('--dk-edge', 'GrayText') }
}

function DocxFrame({ item, t, buf, onReady, onFail, full }) {
  const frame = useRef(null)
  const srcDoc = useMemo(() => docxFrameSource(paneColors()), [])
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  const fit = useCallback(async () => {
    const { fitDocx } = await import('../../utils/officeDocx')
    if (alive.current && frame.current) frame.current.style.height = `${fitDocx(frame.current, full) || 480}px`
  }, [full])
  // Full size or fit to the pane, when the person switches.
  useEffect(() => { fit() }, [fit])
  const draw = useCallback(async () => {
    try {
      const { renderDocx } = await import('../../utils/officeDocx')
      await renderDocx(buf, frame.current)
      if (!alive.current) return
      await fit()
      onReady()
    } catch (error) {
      if (alive.current) onFail('error', error)
    }
  }, [buf, fit, onReady, onFail])
  useEffect(() => {
    const el = frame.current
    if (!el || typeof ResizeObserver === 'undefined') return undefined
    let width = el.clientWidth
    const ro = new ResizeObserver(() => {
      if (el.clientWidth !== width) { width = el.clientWidth; fit() }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [fit])
  return (
    <iframe
      ref={frame} className="pv-frame pv-frame--docx" sandbox="allow-same-origin"
      referrerPolicy="no-referrer" srcDoc={srcDoc} onLoad={draw}
      title={t('preview.htmlTitle', { name: item.name })} data-testid="preview-docx"
    />
  )
}

function SheetGrid({ item, t, buf, onReady, onFail }) {
  const [data, setData] = useState(null)
  useEffect(() => {
    let alive = true
    import('../../utils/officeXlsx')
      .then(m => m.readWorkbookGrid(buf))
      .then((grid) => { if (alive) { setData(grid); onReady() } })
      .catch((error) => { if (alive) onFail('error', error) })
    return () => { alive = false }
  }, [buf, onReady, onFail])
  return data ? <TableView data={data} item={item} t={t} /> : null
}

function SlideDeck({ item, t, buf, onReady, onFail }) {
  const canvas = useRef(null)
  const deck = useRef(null)
  const [at, setAt] = useState(0)
  const [count, setCount] = useState(0)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let alive = true
    const el = canvas.current
    const width = Math.max(320, Math.round((el.parentElement?.clientWidth || 640) * (window.devicePixelRatio || 1)))
    import('../../utils/officeSlides')
      .then(m => m.openSlides(buf, el, width))
      .then((d) => {
        if (!alive) { d.close(); return }
        deck.current = d
        setCount(d.count)
        onReady()
      })
      .catch((error) => { if (alive) onFail('error', error) })
    return () => { alive = false; deck.current?.close(); deck.current = null }
  }, [buf, onReady, onFail])
  const go = async (next) => {
    const d = deck.current
    if (!d || busy || next < 0 || next >= d.count) return
    setBusy(true)
    try { await d.show(next); setAt(next) } catch (error) { onFail('error', error) } finally { setBusy(false) }
  }
  const onKey = (e) => {
    if (e.key === 'ArrowRight' || e.key === 'PageDown') { e.preventDefault(); go(at + 1) }
    if (e.key === 'ArrowLeft' || e.key === 'PageUp') { e.preventDefault(); go(at - 1) }
  }
  return (
    <div className="pv-deck" data-testid="preview-deck">
      <div className="pv-deck__stage" tabIndex={0} onKeyDown={onKey} role="group" aria-label={t('preview.slide', { n: at + 1 })}>
        <canvas ref={canvas} className="pv-deck__canvas" role="img" aria-label={item.name} />
      </div>
      {count > 0 && (
        <div className="pv-deck__nav">
          <Button size="sm" icon="chevron-left" onClick={() => go(at - 1)} disabled={busy || at === 0} aria-label={t('preview.prevSlide')} title={t('preview.prevSlide')} data-testid="preview-slide-prev" />
          <span className="pv-deck__count" role="status" data-testid="preview-slide-count">{t('preview.slideOf', { n: at + 1, total: count })}</span>
          <Button size="sm" icon="chevron-right" onClick={() => go(at + 1)} disabled={busy || at >= count - 1} aria-label={t('preview.nextSlide')} title={t('preview.nextSlide')} data-testid="preview-slide-next" />
        </div>
      )}
    </div>
  )
}
