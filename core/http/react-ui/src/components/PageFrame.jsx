import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { pagesApi } from '../utils/api'
import { buildPageSrcdoc, createPageHost, noticeColours } from '../utils/pagesBridge'
import { MAX_INLINE_TOTAL, inlinePageAssets, pageRefs } from '../utils/pageAssets'
import { BEAT_MS, createFrameWatch } from '../utils/frameWatch'

// One page of the workspace in its sandbox: an iframe with scripts and nothing
// else (an opaque origin: no cookies, storage or DOM of LocalAI, no navigation
// of the window, no pop-ups, no forms) and a policy that reaches no network.
// When the frame loads, the host hands it one end of a MessageChannel; the
// page's window.localai talks over it to createPageHost, which answers through
// the pages API with the viewer's rights. The parent never listens to
// window messages, so another frame cannot speak for the page.
//
// A second load of one document means the page navigated its frame away:
// the bridge closes and onNavigated is called. The caller remounts the frame
// (another key) to start the page again.
//
//   onSend(text)   the page asks to send a message; resolves to {sent, chat_id}
//   onWrite(path)  a write of the page succeeded (path inside the page folder)
//   files          the bridge's file calls {read(path), write(path, content,
//                  base), list(dir), readImage(path)}; the default reaches the
//                  user's own page. readImage is used only to put images in
//   onLink(target) the page asks to open a link: a checked http(s) target of
//                  pageLinkTarget; resolves to {opened} after the person chose
//   codeFiles      a shared page: the files of code its consent covers; no
//                  other text file goes into the page
//   mode, size     'page' (the default), 'widget' or 'area' (the page area of
//                  Home, with no size), and the size of a tile (compact,
//                  small, medium, large): the page reads them as
//                  localai.page.mode and .size. A change after the load goes
//                  to the page as a 'mode' event: the frame is not reloaded
//   budget         a tile, the page area or the dock: the frame is watched (utils/frameWatch.js) and
//                  taken down when it spins or stops answering; onStopped
//                  (reason) is called, and the caller offers Reload
//   onReady()      the first beat of the page came: its script runs and keeps
//                  the thread free. Needs budget; the shell's signal that the
//                  page started, never the page's own claim
//   onScriptError  the page's script threw (the error is the bridge's own
//                  report): the page area takes it as a failed page
//   visible        false while the frame is out of sight (a tile scrolled
//                  away): with a hidden tab, the page gets a visibility event
//                  ({visible}) so it can pause its timers
//   (errors)       the page's script errors are recorded for the assistant
//                  (page_errors) only when the page is the user's own: no
//                  files and no codeFiles. They never go to the chat.
//   onRefused(err) a bridge call was refused because the page may no longer
//                  run (a shared page whose consent was revoked): the caller
//                  takes the frame down
const plain = (html) => ({ html, head: '', modules: false, notes: [], tooLarge: false })

const PageFrame = forwardRef(function PageFrame({ page, html, theme, title, onSend, onWrite, onNavigated, files, onRefused, onLink, onStopped, onReady, onScriptError, codeFiles = null, mode = 'page', size = '', budget = false, className = '', visible = true }, ref) {
  const frameRef = useRef(null)
  const live = useRef({ host: null, channel: null, loads: 0, doc: '', watch: null, timer: 0 })
  const handlers = useRef({ onSend, onWrite, onNavigated, files, onRefused, onLink, onStopped, onReady, onScriptError })
  handlers.current = { onSend, onWrite, onNavigated, files, onRefused, onLink, onStopped, onReady, onScriptError }
  // The mode the frame shows now. The document is built with the mode of the
  // moment; a later change is sent to the page, so a tile that grows into a
  // card keeps its state.
  const modeNow = useRef({ mode, size })
  modeNow.current = { mode, size }
  const sentMode = useRef(null)
  const builtMode = useRef(null)

  const { t } = useTranslation('pages')
  const [build, setBuild] = useState(0)
  // The last built files of this page; a build again keeps showing the last
  // one, so a change that leaves the document as it was does not reload it.
  const [assets, setAssets] = useState(null) // {base, result}
  const deps = useRef(new Set())

  // The calls that read the page's files, with the guard of a refused page.
  const reads = () => {
    const name = page.name
    const f = handlers.current.files
    return {
      read: (path) => guarded(() => (f ? f.read(path) : pagesApi.read(name, path))),
      readImage: (path) => guarded(() => (f ? (f.readImage ? f.readImage(path) : Promise.reject(Object.assign(new Error('images are not available here'), { status: 415 }))) : pagesApi.readImage(name, path))),
    }
  }
  const guarded = (call) => call().catch((e) => {
    if (e?.status === 403 && /page_not_allowed|shared_pages_off/.test(e?.body?.error?.type || '')) {
      setTimeout(() => handlers.current.onRefused?.(e), 0)
    }
    throw e
  })

  const needsFiles = useMemo(() => pageRefs(html), [html])
  const allowed = useMemo(() => (Array.isArray(codeFiles) ? new Set(codeFiles) : null), [codeFiles?.join('\u0000')]) // eslint-disable-line react-hooks/exhaustive-deps
  const base = `${page.name}\u0000${html}\u0000${codeFiles ? codeFiles.join('\u0000') : '*'}`
  useEffect(() => {
    if (!needsFiles) {
      deps.current = new Set()
      return undefined
    }
    let gone = false
    inlinePageAssets(html, reads(), { allowText: allowed ? (path) => allowed.has(path) : null }).then((result) => {
      if (gone) return
      deps.current = new Set(result.deps)
      setAssets({ base, result })
    })
    return () => { gone = true }
  }, [base, build, needsFiles, files]) // eslint-disable-line react-hooks/exhaustive-deps

  // The same object while the page is the same, so the document below is
  // built again only when the page, its files or the theme change, never for
  // a new mode or size: those go to the page as an event.
  const plainDoc = useMemo(() => plain(html), [html])
  const ready = !needsFiles ? plainDoc : assets?.base === base ? assets.result : null
  const srcDoc = useMemo(() => {
    if (!ready) return null
    const failed = [...new Set(ready.notes.filter((n) => n.level === 'error').map((n) => n.path))]
    const banner = ready.tooLarge
      ? t('frame.tooLarge', { limit: `${MAX_INLINE_TOTAL >> 20} MiB` })
      : failed.length ? t('frame.filesFailed', { files: failed.slice(0, 5).join(', '), count: failed.length }) : ''
    const m = { ...modeNow.current }
    builtMode.current = m
    return buildPageSrcdoc(ready.html, {
      name: page.name, title: page.title, theme, head: ready.head, modules: ready.modules,
      notes: ready.notes, banner, dismiss: t('frame.dismiss'), mode: m.mode, size: m.size, colors: noticeColours(), beat: budget,
    })
  }, [ready, page.name, page.title, theme, t, budget])

  useImperativeHandle(ref, () => ({
    emit: (event, data) => {
      // A changed script, style or image of the page builds it again.
      if (event === 'files.changed' && deps.current.size > 0 &&
        (data?.resync || (data?.paths || []).some((p) => deps.current.has(p)))) {
        setBuild((n) => n + 1)
      }
      live.current.host?.emit(event, data)
    },
  }), [])

  const shut = () => {
    live.current.host?.close()
    live.current.channel?.port1.close()
    clearInterval(live.current.timer)
    live.current.host = null
    live.current.channel = null
    live.current.watch = null
    live.current.timer = 0
  }

  // A change of mode or size after the load goes to the page as an event.
  useEffect(() => {
    const sent = sentMode.current
    if (!live.current.host || !sent || (sent.mode === mode && sent.size === size)) return
    sentMode.current = { mode, size }
    live.current.host.emit('mode', { mode, size })
  }, [mode, size])

  useEffect(() => shut, [])

  // The page hears when it goes out of sight and back: the tab is hidden, or
  // its tile scrolled away.
  const seen = useRef(true)
  const visibleRef = useRef(visible)
  visibleRef.current = visible
  const tell = () => {
    const now = visibleRef.current && !document.hidden
    if (now === seen.current) return
    seen.current = now
    // Back in sight: the silence of a slowed frame does not count.
    if (now) live.current.watch?.resume()
    live.current.host?.emit('visibility', { visible: now })
  }
  useEffect(() => {
    tell()
    document.addEventListener('visibilitychange', tell)
    return () => document.removeEventListener('visibilitychange', tell)
  }, [visible])

  const onLoad = () => {
    const frame = frameRef.current
    // A new document (the page changed, or the theme) starts the count again.
    if (live.current.doc !== srcDoc) {
      live.current.doc = srcDoc
      live.current.loads = 0
    }
    live.current.loads++
    shut()
    if (live.current.loads > 1) {
      handlers.current.onNavigated?.()
      return
    }
    const name = page.name
    const channel = new MessageChannel()
    const own = {
      read: (path) => pagesApi.read(name, path),
      write: (path, content, base) => pagesApi.write(name, path, content, base),
      list: async (dir) => (await pagesApi.files(name, dir)).entries || [],
    }
    const guard = guarded
    const watch = budget ? createFrameWatch() : null
    let beaten = false
    const host = createPageHost({
      onBeat: watch ? (busy) => {
        watch.beat(busy)
        if (!beaten) {
          beaten = true
          handlers.current.onReady?.()
        }
      } : undefined,
      post: (m) => channel.port1.postMessage(m),
      files: {
        read: (path) => guard(() => (handlers.current.files || own).read(path)),
        write: async (path, content, base) => {
          const out = await guard(() => (handlers.current.files || own).write(path, content, base))
          handlers.current.onWrite?.(out.path)
          return out
        },
        list: (dir) => guard(() => (handlers.current.files || own).list(dir)),
      },
      chat: { send: (text) => handlers.current.onSend(text) },
      links: { open: (target) => (handlers.current.onLink ? handlers.current.onLink(target) : { opened: false }) },
      // Only the owner's own run records: a viewer of a shared page is not
      // its owner, and the errors are kept in the owner's workspace.
      errors: handlers.current.files || codeFiles !== null
        ? (handlers.current.onScriptError ? { report: (e) => { handlers.current.onScriptError(e); return Promise.resolve() } } : undefined)
        : { report: (e) => { handlers.current.onScriptError?.(e); return pagesApi.reportError(name, e) } },
    })
    channel.port1.onmessage = (e) => { host.receive(e.data) }
    live.current.host = host
    live.current.channel = channel
    live.current.watch = watch
    if (watch) {
      live.current.timer = setInterval(() => {
        const why = watch.check(visibleRef.current && !document.hidden)
        if (!why) return
        shut()
        handlers.current.onStopped?.(why)
      }, BEAT_MS)
    }
    // The document was built with the mode of that moment; a change since
    // (while the files of the page were read) goes to the page now.
    const built = builtMode.current || modeNow.current
    sentMode.current = { ...modeNow.current }
    if (built.mode !== modeNow.current.mode || built.size !== modeNow.current.size) host.emit('mode', modeNow.current)
    // The frame's origin is opaque, so "*" is the only target that reaches it;
    // the port is what carries the trust, and only this frame gets it.
    frame?.contentWindow?.postMessage({ type: 'localai:init' }, '*', [channel.port2])
    seen.current = true
    tell()
  }

  if (srcDoc === null) {
    if (mode !== 'page') return <div className="skeleton skeleton--block wk-ptile__fill" role="status" aria-label={t('view.loading')} data-testid="page-frame-loading" />
    return <div className="pg-stage__state" role="status" data-testid="page-frame-loading"><Icon name="spinner" spin /> {t('view.loading')}</div>
  }

  return (
    <iframe
      ref={frameRef}
      className={`pg-frame${className ? ` ${className}` : ''}`}
      title={title}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      srcDoc={srcDoc}
      onLoad={onLoad}
      data-testid="page-frame"
    />
  )
})

export default PageFrame
