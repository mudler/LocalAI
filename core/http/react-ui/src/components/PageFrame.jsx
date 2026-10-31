import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { pagesApi } from '../utils/api'
import { buildPageSrcdoc, createPageHost } from '../utils/pagesBridge'
import { MAX_INLINE_TOTAL, inlinePageAssets, pageRefs } from '../utils/pageAssets'

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
//   onRefused(err) a bridge call was refused because the page may no longer
//                  run (a shared page whose consent was revoked): the caller
//                  takes the frame down
const plain = (html) => ({ html, head: '', modules: false, notes: [], tooLarge: false })

const PageFrame = forwardRef(function PageFrame({ page, html, theme, title, onSend, onWrite, onNavigated, files, onRefused, onLink }, ref) {
  const frameRef = useRef(null)
  const live = useRef({ host: null, channel: null, loads: 0, doc: '' })
  const handlers = useRef({ onSend, onWrite, onNavigated, files, onRefused, onLink })
  handlers.current = { onSend, onWrite, onNavigated, files, onRefused, onLink }

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
  const base = `${page.name}\u0000${html}`
  useEffect(() => {
    if (!needsFiles) {
      deps.current = new Set()
      return undefined
    }
    let gone = false
    inlinePageAssets(html, reads()).then((result) => {
      if (gone) return
      deps.current = new Set(result.deps)
      setAssets({ base, result })
    })
    return () => { gone = true }
  }, [base, build, needsFiles, files]) // eslint-disable-line react-hooks/exhaustive-deps

  const ready = !needsFiles ? plain(html) : assets?.base === base ? assets.result : null
  const srcDoc = useMemo(() => {
    if (!ready) return null
    const failed = [...new Set(ready.notes.filter((n) => n.level === 'error').map((n) => n.path))]
    const banner = ready.tooLarge
      ? t('frame.tooLarge', { limit: `${MAX_INLINE_TOTAL >> 20} MiB` })
      : failed.length ? t('frame.filesFailed', { files: failed.slice(0, 5).join(', '), count: failed.length }) : ''
    return buildPageSrcdoc(ready.html, {
      name: page.name, title: page.title, theme, head: ready.head, modules: ready.modules,
      notes: ready.notes, banner, dismiss: t('frame.dismiss'),
    })
  }, [ready, page.name, page.title, theme, t])

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
    live.current.host = null
    live.current.channel = null
  }

  useEffect(() => shut, [])

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
    const host = createPageHost({
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
    })
    channel.port1.onmessage = (e) => { host.receive(e.data) }
    live.current.host = host
    live.current.channel = channel
    // The frame's origin is opaque, so "*" is the only target that reaches it;
    // the port is what carries the trust, and only this frame gets it.
    frame?.contentWindow?.postMessage({ type: 'localai:init' }, '*', [channel.port2])
  }

  if (srcDoc === null) {
    return <div className="pg-stage__state" role="status" data-testid="page-frame-loading"><Icon name="spinner" spin /> {t('view.loading')}</div>
  }

  return (
    <iframe
      ref={frameRef}
      className="pg-frame"
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
