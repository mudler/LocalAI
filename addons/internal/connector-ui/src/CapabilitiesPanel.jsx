/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "What this bot can do": one row per capability for one kind of sender, from
// the host's own check run on the form as it is now (the access preview). A
// blocked row says why in the host's words and offers the one fix. The hook
// that asks for the preview is shared with the readiness list.
import { useCallback, useEffect, useRef, useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'
import { basePath } from '../../host-compat/src/utils/basePath.js'
import { FixNotice, Select } from './controls'
import { connectorsApi } from './api'
import ApprovalsView from './ApprovalsView'
import NoteSaveView from './NoteSaveView'
import { fixAction, remainingChecks, sentenceCase, fixLabelKey, pickSubject, previewBody, previewId, previewKey, senderOptions } from './access'

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

function Row({ d, title, s, ctx }) {
  const fix = d.allowed ? null : d.fix
  const via = d.allowed && d.via ? (d.via.startsWith('role:') ? s('can.via.role', { role: d.via.slice(5) }) : ['paired', 'allowlist', 'channel'].includes(d.via) ? s(`can.via.${d.via}`) : d.via === 'agent-connection' ? s('can.via.agentConnection') : '') : ''
  return (
    <li className="cn-can__row" data-testid={`connector-can-${d.capability}`} data-allowed={d.allowed ? 'true' : 'false'}>
      <span className={`cn-can__state cn-can__state--${d.allowed ? 'yes' : 'no'}`}>
        <Icon name={d.allowed ? 'check-circle' : 'alert-circle'} />
        <span>{d.allowed ? s('can.allowed') : s('can.blocked')}</span>
      </span>
      <div className="cn-can__text">
        <span className="cn-can__title">{title(d.capability)}</span>
        {d.allowed ? (via && <span className="cn-muted">{via}</span>) : (<>
          <span className="cn-can__why" data-testid="connector-can-why">{d.message || d.reason || s('can.noMessage')}</span>
          {remainingChecks(d.then_check).length > 0 && (
            <span className="cn-muted cn-can__then">{s('can.then', { list: remainingChecks(d.then_check).map((x) => s(`can.then.${x}`)).join(', ') })}</span>
          )}
        </>)}
      </div>
      {fix && <div className="cn-can__fix"><FixButton fix={fix} ctx={ctx} s={s} /></div>}
    </li>
  )
}

export default function CapabilitiesPanel({ form, kindInfo, capabilities, lists, preview, dirty, s, ctx }) {
  const options = senderOptions(form, kindInfo, lists)
  const [choice, setChoice] = useState('listed')
  const selected = options.some((o) => o.id === choice) ? choice : options[0].id
  const title = (name) => sentenceCase((capabilities || []).find((c) => c.name === name)?.title || name)
  const label = (o) => (o.key === 'role' ? s('can.sender.role', { title: o.title }) : s(`can.sender.${o.key}`))
  const subject = pickSubject(preview.view, selected)
  const decisions = subject?.decisions || []
  const allowed = decisions.filter((d) => d.allowed).length
  const noPerson = selected === 'listed' && !form.accept_any && (ctx.people === 0)
  const loading = preview.status === 'loading'
  return (
    <fieldset className="cn-group cn-can" data-testid="connector-can" aria-busy={loading ? 'true' : undefined}>
      <legend>{s('can.title')}</legend>
      <p className="dk-hint">{s('can.help')}</p>
      <div className="dk-field">
        <label className="dk-label" htmlFor="cn-can-sender">{s('can.sender')}</label>
        <Select id="cn-can-sender" data-testid="connector-can-sender" value={selected} onChange={(e) => setChoice(e.target.value)}>
          {options.map((o) => <option key={o.id} value={o.id}>{label(o)}</option>)}
        </Select>
      </div>
      {dirty && <p className="cn-muted" data-testid="connector-can-dirty">{s(form.id ? 'can.fromForm' : 'can.fromFormNew')}</p>}
      <p className="cn-muted cn-can__live" role="status" aria-live="polite" data-testid="connector-can-status">
        {loading ? s(preview.view ? 'can.stale' : 'can.loading') : ''}
      </p>
      {preview.status === 'error' && (
        <FixNotice severity="error" title={s('can.errorTitle')} reason={preview.error} testId="connector-can-error"
          fix={{ label: s('can.retry'), onClick: preview.reload, testId: 'connector-can-retry' }} />
      )}
      {preview.status !== 'error' && preview.view && (<>
        {noPerson && <p className="cn-muted" data-testid="connector-can-noperson">{s('can.noPerson')}</p>}
        <p className="cn-muted" data-testid="connector-can-summary">{s('can.summary', { allowed, total: decisions.length })}</p>
        <ul className="cn-can__list" aria-label={s('can.title')} data-dim={loading ? 'true' : undefined}>
          {decisions.map((d) => <Row key={d.capability} d={d} title={title} s={s} ctx={ctx} />)}
        </ul>
        <NoteSaveView subjects={subject ? [subject] : []} s={s} testId="connector-can-notesave" />
        <ApprovalsView approvals={preview.view.approvals} s={s} sender={selected} testId="connector-can-approvals" />
      </>)}
    </fieldset>
  )
}
