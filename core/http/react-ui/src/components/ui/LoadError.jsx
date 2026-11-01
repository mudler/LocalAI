import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Callout from './Callout'
import Button from './Button'
import { errorMessage } from '../../utils/loadState'

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
export default function LoadError({ title, error, nothingChanged = true, onRetry, className = '', testId = 'load-error', children }) {
  const { t } = useTranslation('common')
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
      title={title || t('ui.loadError.title')}
      className={`ui-load-error ${className}`.trim()}
      testId={testId}
      actions={onRetry && (
        <Button size="sm" icon="refresh" busy={busy} onClick={retry}>{t('ui.tryAgain')}</Button>
      )}
    >
      {message && <span className="ui-load-error__server">{t('ui.loadError.serverSaid', { message })}</span>}
      {message && nothingChanged ? ' ' : null}
      {nothingChanged && <span>{t('ui.loadError.nothingChanged')}</span>}
      {children}
    </Callout>
  )
}
