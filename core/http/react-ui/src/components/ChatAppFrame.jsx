import { useEffect, useMemo, useRef, useState } from 'react'
import { chatAppsApi } from '../utils/api'
import { appText, buildSrcdoc, createHost, linkLabel } from '../utils/chatApps'

const MAX_SUGGESTIONS = 5
const MAX_LINKS = 3

let nextKey = 0

// One MCP App of a chat, shown under the tool line that produced it. The page is
// read through the chat's resource route and runs in a frame that has a script
// engine and nothing else: no same-origin access, no top navigation, no network
// beyond what the page declared. The frame talks to createHost, which answers
// with the two routes of the chat. Only messages from this frame's window are
// read. When the page cannot load, the result is shown as text.
export default function ChatAppFrame({ chatId, app, onSend, t }) {
  const [page, setPage] = useState(null)
  const [failed, setFailed] = useState('')
  const [suggestions, setSuggestions] = useState([])
  const [links, setLinks] = useState([])
  const frameRef = useRef(null)

  useEffect(() => {
    let live = true
    setPage(null)
    setFailed('')
    chatAppsApi.resource(chatId, app.server, app.resource_uri)
      .then((res) => { if (live) setPage({ html: res.html || '', meta: res.meta || {} }) })
      .catch((e) => { if (live) setFailed(e?.message || 'error') })
    return () => { live = false }
  }, [chatId, app.server, app.resource_uri])

  const srcDoc = useMemo(() => (page ? buildSrcdoc(page.html, page.meta) : ''), [page])

  useEffect(() => {
    const frame = frameRef.current
    if (!srcDoc || !frame) return undefined
    const host = createHost({
      post: (message) => frame.contentWindow?.postMessage(message, '*'), // the frame's origin is opaque
      input: app.input,
      result: app.result,
      callTool: async (name, args) => (await chatAppsApi.call(chatId, app.server, name, args)).result,
      onMessage: (text) => setSuggestions((list) => (list.length >= MAX_SUGGESTIONS ? list : [...list, { key: ++nextKey, text }])),
      onOpenLink: (url) => setLinks((list) => (list.length >= MAX_LINKS ? list : [...list, { key: ++nextKey, url }])),
      onSize: (height) => { frame.style.height = `${height}px` },
      theme: document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light',
    })
    const listen = (event) => {
      if (event.source !== frame.contentWindow) return
      host.receive(event.data).catch(() => {})
    }
    window.addEventListener('message', listen)
    return () => { window.removeEventListener('message', listen); host.close() }
  // The input and result belong to one finished call and never change; the page
  // is rebuilt only when it is another page.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [srcDoc, chatId, app.server])

  const drop = (setter, key) => setter((list) => list.filter((x) => x.key !== key))
  const fallback = appText(app.result)

  return (
    <div className="ch-app" data-testid="chat-app">
      {!page && !failed && <div className="ch-app__state">{t('app.loading')}</div>}
      {failed && (
        <div className="ch-app__state" data-testid="chat-app-fallback">
          <div>{t('app.unavailable', { reason: failed })}</div>
          {fallback && <pre className="ch-app__text">{fallback}</pre>}
        </div>
      )}
      {page && (
        <iframe
          ref={frameRef}
          className={`ch-app__frame${page.meta?.prefersBorder ? ' ch-app__frame--bordered' : ''}`}
          title={t('app.title', { tool: app.tool })}
          sandbox="allow-scripts"
          referrerPolicy="no-referrer"
          srcDoc={srcDoc}
          data-testid="chat-app-frame"
        />
      )}
      {suggestions.map((s) => (
        <div key={s.key} className="ch-app__ask" data-testid="chat-app-suggestion">
          <span className="ch-app__ask-label">{t('app.suggests')}</span>
          <q className="ch-app__ask-text">{s.text}</q>
          <span className="ch-pending__actions">
            <button type="button" className="btn btn-primary btn-sm" onClick={() => { drop(setSuggestions, s.key); onSend?.(s.text) }}>{t('app.send')}</button>
            <button type="button" className="ch-link" onClick={() => drop(setSuggestions, s.key)}>{t('app.dismiss')}</button>
          </span>
        </div>
      ))}
      {links.map((l) => (
        <div key={l.key} className="ch-app__ask" data-testid="chat-app-link">
          <span className="ch-app__ask-label">{t('app.wantsLink', { host: linkLabel(l.url) })}</span>
          <span className="ch-pending__actions">
            <a className="btn btn-primary btn-sm" href={l.url} target="_blank" rel="noopener noreferrer" onClick={() => drop(setLinks, l.key)}>{t('app.open')}</a>
            <button type="button" className="ch-link" onClick={() => drop(setLinks, l.key)}>{t('app.dismiss')}</button>
          </span>
        </div>
      ))}
    </div>
  )
}
