// Small building blocks over plain elements and the host's wrappers. All
// class names start with tbl-. Dialog content carries the add-on's root
// attribute because a host dialog is drawn outside the page root, and the
// add-on's sheet is scoped to that attribute.
import { describeError } from './lib/errors.js'

export function createUi(ctx) {
  const { sdk, h, R, Fragment, t } = ctx
  const { Button, Spinner, EmptyState, Badge } = sdk.ui

  function Root({ children, className }) {
    // The outer element carries the add-on's root attribute, so the scoped
    // sheet reaches the inner element too, also inside a host dialog.
    return (
      <div className="tbl-scope" data-addon={sdk.addonId}>
        <div className={className ? `tbl-root ${className}` : 'tbl-root'}>{children}</div>
      </div>
    )
  }

  function Field({ label, hint, error, children, id }) {
    return (
      <div className={error ? 'tbl-field tbl-field--error' : 'tbl-field'}>
        <label className="tbl-label" htmlFor={id}>{label}</label>
        {children}
        {hint && !error ? <p className="tbl-hint">{hint}</p> : null}
        {error ? <p className="tbl-error" role="alert">{error}</p> : null}
      </div>
    )
  }

  function Banner({ tone = 'info', children, actions, testid }) {
    return (
      <div className={`tbl-banner tbl-banner--${tone}`} role={tone === 'error' || tone === 'warn' ? 'alert' : 'status'} data-testid={testid}>
        <span className="tbl-banner__text">{children}</span>
        {actions ? <span className="tbl-banner__actions">{actions}</span> : null}
      </div>
    )
  }

  // What went wrong and the one recovery that fits: reload for a stale view,
  // try again for an outage, check again for an unknown outcome.
  function Problem({ error, onRetry, onReload, onCheck }) {
    const d = describeError(error)
    let action = null
    if (d.kind === 'stale' && onReload) action = <Button size="sm" onClick={onReload}>{t('error.reloadRetry')}</Button>
    else if (d.kind === 'unknown' && (onCheck || onRetry)) action = <Button size="sm" onClick={onCheck || onRetry}>{t('error.checkAgain')}</Button>
    else if (d.retry && onRetry) action = <Button size="sm" onClick={onRetry}>{t('common.retry')}</Button>
    // A problem raised inside a long form may sit below the fold: bring it
    // into view once, so the person is never left guessing why nothing moved.
    const box = R.useRef(null)
    R.useEffect(() => {
      const el = box.current
      if (el && typeof el.scrollIntoView === 'function') el.scrollIntoView({ block: 'nearest' })
    }, [error])
    return <div ref={box}><Banner tone="error" testid="tbl-error" actions={action}>{t(d.key)}</Banner></div>
  }

  function RoleBadge({ role }) {
    const tone = role === 'owner' ? 'healthy' : role === 'editor' ? 'loading' : 'idle'
    const label = t(`role.${role === 'owner' || role === 'editor' ? role : 'viewer'}`)
    return <span className="tbl-role" data-role={role} data-testid="tbl-role"><Badge tone={tone}>{label}</Badge></span>
  }

  function Loading({ label }) {
    return <div className="tbl-loading" role="status" aria-live="polite" data-testid="tbl-loading"><Spinner size="sm" /><span>{label || t('common.loading')}</span></div>
  }

  // A link that navigates inside the add-on's own pages.
  function Link({ to, children, className, testid, label }) {
    let href = '#'
    try { href = sdk.navigation.href(to) } catch { href = '#' }
    return (
      <a
        className={className || 'tbl-link'}
        href={href}
        data-testid={testid}
        aria-label={label}
        onClick={(e) => {
          if (e.metaKey || e.ctrlKey || e.shiftKey || e.button === 1) return
          e.preventDefault()
          try { sdk.navigation.navigate(to) } catch { /* an undeclared page never resolves */ }
        }}
      >{children}</a>
    )
  }

  return { Root, Field, Banner, Problem, RoleBadge, Loading, Link, Button, Spinner, EmptyState, Badge, Fragment, h }
}
