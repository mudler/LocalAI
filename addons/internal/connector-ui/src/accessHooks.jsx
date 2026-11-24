/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// Helpers of the access check that the matrix and the status line share: the
// jump to a field, the hook that asks the host what the form will do (the
// access preview), and the button of a fix.
import { useCallback, useEffect, useRef, useState } from 'react'
import { basePath } from '../../host-compat/src/utils/basePath.js'
import { connectorsApi } from './api'
import { fixAction, fixLabelKey, previewBody, previewId, previewKey } from './access'

const FLASH_MS = 1600

// Scrolls to the first of the selectors that exists on the page, focuses it
// (or the first control inside it) and marks it for a moment.
export function jumpTo(selectors) {
  for (const sel of selectors) {
    const el = document.querySelector(sel)
    if (!el) continue
    el.scrollIntoView?.({ block: 'center' })
    const focusable = el.matches?.('button:not([disabled]), input, select, textarea, a[href]') ? el : el.querySelector?.('button:not([disabled]), input, select, textarea')
    ;(focusable || el).focus?.({ preventScroll: true })
    const mark = el.closest?.('li, fieldset, .dk-field, .cn-sub') || el
    mark.setAttribute('data-jumped', 'true')
    setTimeout(() => mark.removeAttribute('data-jumped'), FLASH_MS)
    return true
  }
  return false
}

// Whether the person is an administrator: true, false, or null when this host
// does not say (an older host). The server decides every call anyway.
export function useIsAdmin(useAuth) {
  try { return Boolean(useAuth().isAdmin) } catch (_e) { return null }
}

/**
 * Asks the host what the form will do, 400 ms after the last change, and drops
 * the answer of a request that is already stale.
 * @returns {{ status: 'idle'|'loading'|'ready'|'error', view: object|null, error: string, reload: Function, settled: boolean }}
 */
export function useAccessPreview({ form, kind, enabled, version = 0 }) {
  const [state, setState] = useState({ status: 'idle', view: null, error: '', key: '' })
  const [again, setAgain] = useState(0)
  const key = enabled && form ? previewKey(form, kind) : ''
  const latest = useRef(form)
  latest.current = form
  useEffect(() => {
    if (!key) { setState({ status: 'idle', view: null, error: '', key: '' }); return undefined }
    setState((s) => ({ ...s, status: 'loading', error: '' }))
    const ctl = new AbortController()
    const timer = setTimeout(async () => {
      try {
        const f = latest.current
        const view = await connectorsApi.preview(previewId(f), previewBody(f, kind), ctl.signal)
        if (!ctl.signal.aborted) setState({ status: 'ready', view, error: '', key })
      } catch (e) {
        if (ctl.signal.aborted || e?.name === 'AbortError') return
        setState((s) => ({ ...s, status: 'error', error: e?.message || '' }))
      }
    }, 400)
    return () => { clearTimeout(timer); ctl.abort() }
  }, [key, kind, version, again])
  const reload = useCallback(() => setAgain((n) => n + 1), [])
  return { status: state.status, view: state.view, error: state.error, reload }
}

// The button or link of a fix. A link to another page opens in a new tab, so
// a form with unsaved changes stays as it is.
export function FixButton({ fix, ctx, s, onTest, onSave, variant = 'secondary' }) {
  const action = fixAction(fix, ctx)
  if (!action) return null
  const cls = `dk-btn dk-btn--${variant} dk-btn--sm cn-fix`
  const label = s(fixLabelKey(fix))
  if (action.type === 'text') return <span className="cn-muted cn-fix__text" data-testid="connector-fix-text">{s(`fix.${action.key}`)}</span>
  if (action.type === 'link') {
    return (
      <a className={cls} href={basePath + action.to} target="_blank" rel="noopener noreferrer" data-testid="connector-fix">
        {label}<span className="cn-vh"> {s('fix.newTab')}</span>
      </a>
    )
  }
  if (action.type === 'save') {
    // The same as the Save button, when it can save; else show the button and why.
    const save = () => {
      const b = document.querySelector(action.selectors[0])
      if (b && !b.disabled) b.click()
      else jumpTo(action.selectors)
    }
    return <button type="button" className={cls} data-testid="connector-fix" onClick={save}>{label}</button>
  }
  if (action.type === 'test') return <button type="button" className={cls} data-testid="connector-fix" onClick={onTest}>{label}</button>
  return <button type="button" className={cls} data-testid="connector-fix" onClick={() => jumpTo(action.selectors)}>{label}</button>
}
