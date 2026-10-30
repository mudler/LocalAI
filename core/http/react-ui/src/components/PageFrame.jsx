import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef } from 'react'
import { pagesApi } from '../utils/api'
import { buildPageSrcdoc, createPageHost } from '../utils/pagesBridge'

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
//                  base), list(dir)}; the default reaches the user's own page
//   onRefused(err) a bridge call was refused because the page may no longer
//                  run (a shared page whose consent was revoked): the caller
//                  takes the frame down
const PageFrame = forwardRef(function PageFrame({ page, html, theme, title, onSend, onWrite, onNavigated, files, onRefused }, ref) {
  const frameRef = useRef(null)
  const live = useRef({ host: null, channel: null, loads: 0, doc: '' })
  const handlers = useRef({ onSend, onWrite, onNavigated, files, onRefused })
  handlers.current = { onSend, onWrite, onNavigated, files, onRefused }

  const srcDoc = useMemo(
    () => buildPageSrcdoc(html, { name: page.name, title: page.title, theme }),
    [html, page.name, page.title, theme],
  )

  useImperativeHandle(ref, () => ({
    emit: (event, data) => live.current.host?.emit(event, data),
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
    // A refusal that means the page may not run any more ends the frame at
    // once; the page sees the error of its call first.
    const guard = (call) => call().catch((e) => {
      if (e?.status === 403 && /page_not_allowed|shared_pages_off/.test(e?.body?.error?.type || '')) {
        setTimeout(() => handlers.current.onRefused?.(e), 0)
      }
      throw e
    })
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
    })
    channel.port1.onmessage = (e) => { host.receive(e.data) }
    live.current.host = host
    live.current.channel = channel
    // The frame's origin is opaque, so "*" is the only target that reaches it;
    // the port is what carries the trust, and only this frame gets it.
    frame?.contentWindow?.postMessage({ type: 'localai:init' }, '*', [channel.port2])
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
