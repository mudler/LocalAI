/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, Navigate, useBlocker, useLocation, useOutletContext, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import CatalogEmpty from '../components/catalog/CatalogEmpty'
import LoadingSpinner from '../components/LoadingSpinner'
import ConfirmDialog from '../components/ConfirmDialog'
import Icon from '../components/Icon'
import { useAuth } from '../context/AuthContext'
import { addonsApi } from '../utils/addonsClient'
import { addonDisplayName, appActions, localizedText } from '../components/addons/addonState'
import { describeOutcome } from '../components/addons/errors'
import { useAddonActions } from '../components/addons/useAddonActions'
import AddonErrorBoundary from './AddonErrorBoundary'
import { useAddonRegistry } from './useAddonRegistry'
import { appStatus } from './registryState'
import { resolveSubpath } from './sdk'
import { declaresSlot } from './slots'
import { addonSettingsAddress } from '../utils/addonSettings'
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
  // The add-on's own name. A list entry with no title carries its id as the
  // title, and an add-on that is not listed has only its id: both say the id
  // with a capital first letter, never the bare identifier.
  const listedTitle = app ? localizedText(app, 'title', i18n.language) : ''
  const title = listedTitle && listedTitle !== addonId ? listedTitle : addonDisplayName(addonId)
  const { isAdmin } = useAuth()
  // Turning on an add-on from this page is the same action as on My add-ons.
  const actions = useAddonActions(addonsApi, { onSettled: () => { refresh() } })
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

  // The shell names the add-on only where the add-on's page cannot mount (not
  // enabled, failed, no UI, bundle not loaded, no such page). Once the page
  // mounts it draws its own title, even to say it is not enabled on this
  // server, and the shell draws none.
  // What the page says depends on what the add-on is: off (installed and
  // turned off), not installed, or failed. Anything else keeps the generic
  // title and the reason of the shell.
  const unavailable = (reason, extra) => {
    const known = REASONS.includes(reason) ? reason : 'import_failed'
    let heading = t('shell.unavailable.title')
    let text = t(`shell.reasons.${known}`)
    let acts = extra
    let state = ''
    const control = appActions(app)
    if (known === 'disabled' && app) {
      state = 'off'
      heading = t('shell.off.title', { name: title })
      text = control.start ? t('shell.off.body') : t('shell.off.reader')
      const startKey = `start:${addonId}:${app.generation}`
      const working = actions.state[startKey]?.phase === 'working'
      const outcome = actions.state[startKey]
      acts = (
        <>
          {control.start && (
            <button
              type="button"
              className="dk-btn dk-btn--primary dk-btn--sm"
              disabled={working}
              data-testid="addon-turn-on"
              onClick={() => actions.run(startKey, {
                addonId,
                submit: (operationId, signal) => addonsApi.apps.start(addonId, { operationId, generation: app.generation }, signal),
                verify: async (signal) => (await addonsApi.apps.list(signal)).find(a => a.id === addonId)?.enabled === true,
              })}
            >
              <Icon name={working ? 'spinner' : 'toggle-on'} spin={working} /> {t('apps.start')}
            </button>
          )}
          {extra}
          {outcome && outcome.outcome && describeOutcome(outcome.outcome, t, outcome.error) && (
            <span className="ad-card__note" data-tone="error" role="alert">{describeOutcome(outcome.outcome, t, outcome.error)}</span>
          )}
        </>
      )
    } else if (known === 'removed' && !app && registry.allowed) {
      state = 'not-installed'
      heading = t('shell.notInstalled.title', { name: title })
      text = isAdmin ? t('shell.notInstalled.body') : t('shell.notInstalled.reader')
      acts = isAdmin ? <Link className="dk-btn dk-btn--primary dk-btn--sm" to={`/app/addons?tab=browse&q=${encodeURIComponent(addonId)}`} data-testid="addon-find">{t('drawer.browse')}</Link> : null
    } else if (known === 'failed') {
      state = 'failed'
      heading = t('shell.failedTitle', { name: title })
      acts = (
        <>
          {extra}
          {isAdmin && <Link className="dk-btn dk-btn--ghost dk-btn--sm" to={`/app/addons?detail=${encodeURIComponent(addonId)}`} data-testid="addon-details">{t('card.details')}</Link>}
        </>
      )
    }
    return (
      <div className="page ad-page ad-page--shell" data-testid="addon-unavailable" data-reason={reason} data-state={state || undefined}>
        <PageHeader title={title} />
        <CatalogEmpty icon="puzzle" title={heading} text={text} role="status">
          <div className="ad-shell-actions">
            {acts}
            {back}
          </div>
        </CatalogEmpty>
        {leaveDialog}
        {reloadDialog}
      </div>
    )
  }
  const reloadButton = (
    <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onReload} disabled={reloading} data-testid="addon-reload">
      {reloading ? t('shell.reloading') : t('shell.reload')}
    </button>
  )

  if (!registry.allowed) return unavailable('removed')
  if (registry.status === 'idle' || (registry.status === 'error' && !app)) {
    if (registry.status === 'error') return unavailable('import_failed', reloadButton)
    return (
      <div className="page ad-page" data-testid="addon-loading"><LoadingSpinner /></div>
    )
  }
  const isStale = Boolean(instance && instance.status === 'stale')
  if (!app && !isStale) return unavailable(instance && instance.reason ? instance.reason : 'removed', instance ? reloadButton : null)

  // A withdrawn instance: the list already says why, or the instance does.
  if (instance && (instance.status === 'unavailable' || instance.status === 'error')) {
    return unavailable(instance.reason || status, reloadButton)
  }
  if (!isStale && status !== 'ready') return unavailable(status === 'incompatible' ? 'incompatible_sdk' : status, status === 'incompatible' || status === 'disabled' ? null : reloadButton)

  // No page in the address: the main page, or the first declared one.
  if (!isStale && !subpath) {
    const first = app.mainPage ? app.mainPage.split('/').pop() : pages[0]
    if (first) return <Navigate to={`/app/addons/${encodeURIComponent(addonId)}/${encodeURIComponent(first)}`} replace />
  }
  if (!resolved && !isStale) {
    return (
      <div className="page ad-page ad-page--shell" data-testid="addon-missing-page">
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
        <LoadingSpinner />
      </div>
    )
  }
  // One tree for a live page and a frozen one, so the add-on's own state
  // survives the moment it is withdrawn.
  return (
    <div className="page ad-page" data-testid={isStale ? 'addon-stale' : 'addon-page'}>
      {/* The add-on draws its own title; the shell adds none. Its settings
          live on the Settings tab of its details, and the shell links there. */}
      {!isStale && isAdmin && app && declaresSlot(app, 'settings.sections') && (
        <div className="ad-shellbar">
          <Link className="dk-btn dk-btn--ghost dk-btn--sm" to={addonSettingsAddress(addonId)} data-testid="addon-page-settings">
            <Icon name="settings" /> {t('settingsTab.pageLink')}
          </Link>
        </div>
      )}
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
