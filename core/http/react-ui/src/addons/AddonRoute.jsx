/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, Navigate, useBlocker, useLocation, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import CatalogEmpty from '../components/catalog/CatalogEmpty'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import { localizedText } from '../components/addons/addonState'
import AddonErrorBoundary from './AddonErrorBoundary'
import { useAddonRegistry } from './useAddonRegistry'
import { appStatus } from './registryState'
import { resolveSubpath } from './sdk'
import '../components/addons/addons.css'

// The shell route of an add-on page: /app/addons/:addonId/<page>[/...].
//
// The address always names a declared page of the add-on; anything else is the
// "no such page" state. The page component is rendered inside an error
// boundary, in a root element that carries the add-on's id so its scoped
// style sheet reaches it and nothing else. While an add-on is withdrawn the
// shell says why and offers an explicit Reload; unsaved edits are never
// discarded without asking.

const REASONS = ['disabled', 'removed', 'failed', 'updated', 'no-ui', 'incompatible_sdk', 'untrusted_asset', 'invalid_module', 'import_failed', 'create_failed', 'timeout', 'css_failed', 'aborted']

export default function AddonRoute() {
  const { t, i18n } = useTranslation('addons')
  const { addonId, '*': splat } = useParams()
  const location = useLocation()
  const outlet = useOutletContext()
  const registry = useAddonRegistry()
  const { acquire, hold, reload, refresh, setNotifier, setLocation } = registry
  const [confirmReload, setConfirmReload] = useState(false)
  const [reloading, setReloading] = useState(false)

  useEffect(() => acquire(), [acquire])

  const addToast = outlet && outlet.addToast
  useEffect(() => {
    setNotifier((kind, message) => addToast?.(message, kind))
    return () => setNotifier(null)
  }, [setNotifier, addToast])

  const app = registry.apps.find(a => a.id === addonId) || null
  const instance = registry.instances.find(i => i.id === addonId) || null
  const status = appStatus(app)
  const title = (app && localizedText(app, 'title', i18n.language)) || addonId
  const pages = app && app.ui ? app.ui.pages || [] : []
  const subpath = splat || ''
  const resolved = subpath ? resolveSubpath(subpath, pages) : null
  // A withdrawn add-on may no longer be listed: its frozen page keeps the
  // address it was showing.
  const lastResolved = useRef(null)
  if (resolved) lastResolved.current = resolved

  // Hold the instance while this route is open. A withdrawn instance is not
  // replaced here: Reload does that, on the user's say.
  const handle = useRef(null)
  const heldId = useRef('')
  const usableNow = status === 'ready'
  useEffect(() => {
    if (heldId.current !== addonId && handle.current) {
      handle.current.release()
      handle.current = null
    }
    if (usableNow && !handle.current && app) {
      handle.current = hold(app)
      heldId.current = addonId
    }
  }, [addonId, usableNow, app, hold])
  useEffect(() => () => {
    if (handle.current) { handle.current.release(); handle.current = null }
  }, [])

  // The addon asks "where am I"; the shell answers from the address.
  useEffect(() => {
    setLocation(addonId, { page: resolved ? resolved.page : '', path: resolved ? resolved.rest : '', search: location.search.replace(/^\?/, '') })
  }, [setLocation, addonId, resolved, location.search])

  const dirty = Boolean(instance && instance.dirty)
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && currentLocation.pathname !== nextLocation.pathname)
  useEffect(() => {
    if (!dirty) return undefined
    const guard = (e) => { e.preventDefault(); e.returnValue = '' }
    window.addEventListener('beforeunload', guard)
    return () => window.removeEventListener('beforeunload', guard)
  }, [dirty])

  const doReload = useCallback(async () => {
    setConfirmReload(false)
    setReloading(true)
    try {
      const apps = await refresh()
      const fresh = (apps || registry.apps).find(a => a.id === addonId)
      if (fresh && appStatus(fresh) === 'ready') reload(fresh)
    } finally {
      setReloading(false)
    }
  }, [refresh, reload, registry.apps, addonId])

  const onReload = () => {
    if (dirty) setConfirmReload(true)
    else doReload()
  }

  const leaveDialog = (
    <ConfirmDialog
      open={blocker.state === 'blocked'}
      title={t('shell.leave.title')}
      message={t('shell.leave.body')}
      confirmLabel={t('shell.leave.leave')}
      cancelLabel={t('shell.leave.stay')}
      danger
      onConfirm={() => blocker.proceed?.()}
      onCancel={() => blocker.reset?.()}
    />
  )
  const reloadDialog = (
    <ConfirmDialog
      open={confirmReload}
      title={t('shell.discard.title')}
      message={t('shell.discard.body')}
      confirmLabel={t('shell.discard.confirm')}
      cancelLabel={t('shell.discard.cancel')}
      danger
      onConfirm={doReload}
      onCancel={() => setConfirmReload(false)}
    />
  )

  const back = <Link className="dk-btn dk-btn--ghost dk-btn--sm" to="/app/addons">{t('page.back')}</Link>

  const unavailable = (reason, extra) => (
    <div className="page ad-page" data-testid="addon-unavailable" data-reason={reason}>
      <PageHeader title={title} />
      <CatalogEmpty icon="puzzle" title={t('shell.unavailable.title')} text={t(`shell.reasons.${REASONS.includes(reason) ? reason : 'import_failed'}`)} role="status">
        <div className="ad-shell-actions">
          {extra}
          {back}
        </div>
      </CatalogEmpty>
      {leaveDialog}
      {reloadDialog}
    </div>
  )
  const reloadButton = (
    <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onReload} disabled={reloading} data-testid="addon-reload">
      {reloading ? t('shell.reloading') : t('shell.reload')}
    </button>
  )

  if (!registry.allowed) return unavailable('removed')
  if (registry.status === 'idle' || (registry.status === 'error' && !app)) {
    if (registry.status === 'error') return unavailable('import_failed', reloadButton)
    return (
      <div className="page ad-page" data-testid="addon-loading"><PageHeader title={title} /><LoadingSpinner /></div>
    )
  }
  const isStale = Boolean(instance && instance.status === 'stale')
  if (!app && !isStale) return unavailable(instance && instance.reason ? instance.reason : 'removed', instance ? reloadButton : null)

  // A withdrawn instance: the list already says why, or the instance does.
  if (instance && (instance.status === 'unavailable' || instance.status === 'error')) {
    return unavailable(instance.reason || status, reloadButton)
  }
  if (!isStale && status !== 'ready') return unavailable(status === 'incompatible' ? 'incompatible_sdk' : status, status === 'incompatible' ? null : reloadButton)

  // No page in the address: the main page, or the first declared one.
  if (!isStale && !subpath) {
    const first = app.mainPage ? app.mainPage.split('/').pop() : pages[0]
    if (first) return <Navigate to={`/app/addons/${encodeURIComponent(addonId)}/${encodeURIComponent(first)}`} replace />
  }
  if (!resolved && !isStale) {
    return (
      <div className="page ad-page" data-testid="addon-missing-page">
        <PageHeader title={title} />
        <CatalogEmpty icon="puzzle" title={t('shell.pageMissing.title')} text={t('shell.pageMissing.body', { title, page: subpath.split('/')[0] || '' })} role="status">
          <div className="ad-shell-actions">{back}</div>
        </CatalogEmpty>
      </div>
    )
  }
  if (!instance || instance.status === 'loading') {
    return (
      <div className="page ad-page" data-testid="addon-loading">
        <PageHeader title={title} />
        <LoadingSpinner />
      </div>
    )
  }
  // One tree for a live page and a frozen one, so the add-on's own state
  // survives the moment it is withdrawn.
  return (
    <div className="page ad-page" data-testid={isStale ? 'addon-stale' : 'addon-page'}>
      <PageHeader title={title} />
      {isStale && (
        <div className="ad-banner" role="alert">
          <span>{t('shell.stale', { title })}</span>
          {reloadButton}
        </div>
      )}
      <AddonSurface id={addonId} instance={instance} resolved={resolved || lastResolved.current} search={location.search.replace(/^\?/, '')} frozen={isStale} t={t} />
      {leaveDialog}
      {reloadDialog}
    </div>
  )
}

function AddonSurface({ id, instance, resolved, search = '', frozen = false, t }) {
  const Component = instance.Component
  const labels = useMemo(() => ({ title: t('shell.error.title'), text: t('shell.error.text'), retryLabel: t('shell.error.retry') }), [t])
  if (!Component) {
    return <CatalogEmpty icon="puzzle" title={t('shell.noComponent.title')} text={t('shell.noComponent.body')} role="status" />
  }
  return (
    <div
      className={frozen ? 'ad-host ad-host--frozen' : 'ad-host'}
      data-addon={id}
      data-generation={instance.generation}
      data-testid="addon-surface"
    >
      <AddonErrorBoundary key={`${instance.version}:${resolved ? resolved.path : ''}`} labels={labels}>
        <Component page={resolved ? resolved.page : ''} path={resolved ? resolved.rest : ''} search={search} />
      </AddonErrorBoundary>
    </div>
  )
}
