import { useState } from 'react'
import Callout from './Callout.jsx'
import Button from './Button.jsx'
import { errorMessage, LOAD_ERROR_LABELS } from './loadErrorModel.js'

// The error state of a region that failed to load. It replaces the region,
// so a failed load never looks like an empty list, and it is never a bare
// toast. It says what failed (in bold), what the server answered, whether
// anything changed, and offers Try again.
//
//   title           what failed: "Could not load your MCP servers."
//   error           the error or its message; the server's words are shown
//   nothingChanged  adds "Nothing was changed." (true for every plain read)
//   onRetry         called by Try again; a returned promise keeps the
//                   button busy until it settles
//   labels          the words, for a translated app: { title, tryAgain,
//                   nothingChanged, serverSaid(message) }. Each one left out
//                   is the English of LOAD_ERROR_LABELS.
export default function LoadError({ title, error, nothingChanged = true, onRetry, labels, className = '', testId = 'load-error', children }) {
  const words = { ...LOAD_ERROR_LABELS, ...labels }
  const [busy, setBusy] = useState(false)
  const message = errorMessage(error)

  const retry = () => {
    if (!onRetry || busy) return
    const out = onRetry()
    if (out && typeof out.then === 'function') {
      setBusy(true)
      out.finally(() => setBusy(false))
    }
  }

  return (
    <Callout
      tone="error"
      role="alert"
      title={title || words.title}
      className={`ui-load-error ${className}`.trim()}
      testId={testId}
      actions={onRetry && (
        <Button size="sm" icon="refresh" busy={busy} onClick={retry}>{words.tryAgain}</Button>
      )}
    >
      {message && <span className="ui-load-error__server">{words.serverSaid(message)}</span>}
      {message && nothingChanged ? ' ' : null}
      {nothingChanged && <span>{words.nothingChanged}</span>}
      {children}
    </Callout>
  )
}
