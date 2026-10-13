/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { FEATURE_ADDONS } from '../utils/capabilities'
import { basePath } from '../utils/basePath'
import { addonsApi } from '../utils/addonsClient'
import { pollDelay } from '../components/catalog/catalogState'
import { resolveManage, resolveTab } from '../components/addons/addonState'
import Dialog from '../components/Dialog'
import SideSheet from '../components/SideSheet'
import { createHostSDK, ICON_NAMES } from './sdk'
import { loadAddonUI } from './loader'
import { createRuntimeManager } from './runtime'
import { availableChatActions, navigationItems as buildNavigation } from './registryState'
import { host } from './hostServices'
import { RegistryContext } from './registryContext'
import AddonErrorBoundary from './AddonErrorBoundary'
import './shell.css'

// The add-on registry of the shell. It polls the apps list while something
// needs it (the navigation of the Add-ons tab, an add-on page, a chat
// action), keeps one runtime instance per loaded add-on UI and applies every
// list to them: an add-on the owner disabled, removed, restarted or updated
// is withdrawn on the next list, never on the add-on's own say-so.
//
// What an add-on contributes (pages, navigation, chat actions) is stored per
// add-on and generation and read from here by the host; nothing is registered
// anywhere else, so withdrawing an instance removes every slot at once.

const EMPTY = { status: 'idle', apps: [], error: null }

export function AddonRegistryProvider({ children, api = addonsApi }) {
  const { hasFeature, user, isAdmin } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const { t, i18n } = useTranslation('addons')
  const allowed = hasFeature(FEATURE_ADDONS)
  const [list, setList] = useState(EMPTY)
  const [consumers, setConsumers] = useState(0)
  const [layers, setLayers] = useState([])
  const navigateRef = useRef(navigate)
  navigateRef.current = navigate
  const notifierRef = useRef(null)
  const locationsRef = useRef(new Map())
  const listeners = useRef(new Set())
  const tick = useRef(null)
  const layerSeq = useRef(0)

  const manager = useMemo(() => {
    const load = (app, { signal, setDirty }) => loadAddonUI({
      app,
      signal,
      document,
      origin: window.location.origin,
      basePath,
      iconNames: ICON_NAMES,
      createSDK: ({ signal: own }) => createHostSDK({
        addonId: app.id,
        generation: app.generation,
        signal: own,
        pages: app.ui.pages || [],
        host,
        bindings: {
          navigate: (path, options) => navigateRef.current(path, options),
          notify: (kind, message) => notifierRef.current?.(kind, message),
          setDirty,
          current: () => locationsRef.current.get(app.id),
          openLayer: (kind, spec) => {
            const id = ++layerSeq.current
            const layer = { id, addonId: app.id, generation: app.generation, kind, spec }
            setLayers(prev => [...prev, layer])
            return { close: () => setLayers(prev => prev.filter(l => l.id !== id)) }
          },
        },
      }),
    })
    return createRuntimeManager({ load, onChange: () => listeners.current.forEach(fn => fn()) })
  }, [])

  const subscribe = useCallback((fn) => { listeners.current.add(fn); return () => listeners.current.delete(fn) }, [])
  const instances = useSyncExternalStore(subscribe, manager.snapshot, manager.snapshot)

  // The Apps tab reads the same list on its own cadence and feeds it here, so
  // that page is never read twice. Everywhere else the registry reads it.
  const params = new URLSearchParams(location.search)
  const appsTabOwnsList = location.pathname.replace(/\/+$/, '') === '/app/addons'
    && !resolveManage(params, isAdmin) && resolveTab(params.get('tab'), isAdmin) === 'apps'
  const ingest = useCallback((apps) => {
    setList({ status: 'ready', apps, error: null })
    manager.revalidate(apps)
  }, [manager])

  // One poll chain at a time, backing off on failure and slower when hidden.
  const refresh = useCallback(async () => {
    try {
      const apps = await api.apps.list()
      setList({ status: 'ready', apps, error: null })
      manager.revalidate(apps)
      return apps
    } catch (error) {
      setList(prev => ({ status: prev.status === 'ready' ? 'ready' : 'error', apps: prev.apps, error }))
      return null
    }
  }, [api, manager])

  useEffect(() => {
    if (!allowed || consumers === 0 || appsTabOwnsList) return undefined
    let alive = true
    let failures = 0
    let timer = null
    const run = async () => {
      const apps = await refresh()
      if (!alive) return
      failures = apps === null ? failures + 1 : 0
      timer = setTimeout(run, pollDelay(failures, document.hidden))
    }
    tick.current = () => { clearTimeout(timer); if (alive) run() }
    run()
    return () => { alive = false; clearTimeout(timer); tick.current = null }
  }, [allowed, consumers, refresh, appsTabOwnsList])

  // A different user, or a user who lost the feature, never keeps another
  // session's add-on code or caches.
  const userId = user?.id || ''
  useEffect(() => {
    manager.disposeAll()
    setLayers([])
    setList(EMPTY)
  }, [manager, userId, allowed])

  // Layers of a withdrawn add-on close with it.
  useEffect(() => {
    setLayers(prev => {
      const live = prev.filter(l => {
        const inst = instances.find(i => i.id === l.addonId)
        return inst && inst.status === 'ready' && inst.generation === l.generation
      })
      return live.length === prev.length ? prev : live
    })
  }, [instances])

  useEffect(() => () => manager.disposeAll(), [manager])

  const acquire = useCallback(() => {
    let done = false
    setConsumers(n => n + 1)
    return () => { if (!done) { done = true; setConsumers(n => Math.max(0, n - 1)) } }
  }, [])

  const value = useMemo(() => ({
    status: list.status,
    apps: list.apps,
    error: list.error,
    instances,
    allowed,
    refresh,
    ingest,
    acquire,
    hold: (app) => manager.hold(app),
    reload: (app) => manager.reload(app),
    setNotifier: (fn) => { notifierRef.current = fn },
    setLocation: (id, loc) => { locationsRef.current.set(id, loc) },
    navigationItems: buildNavigation(list.apps, { language: i18n.language }),
    chatActions: availableChatActions(instances, list.apps, { hasFeature: () => allowed }),
  }), [list, instances, allowed, refresh, ingest, acquire, manager, i18n.language])

  const boundaryLabels = { title: t('shell.error.title'), text: t('shell.error.text'), retryLabel: t('shell.error.retry') }
  return (
    <RegistryContext.Provider value={value}>
      {children}
      {layers.map(layer => (
        <AddonLayer key={layer.id} layer={layer} labels={boundaryLabels} closeLabel={t('shell.layer.close')}
          onClose={() => setLayers(prev => prev.filter(l => l.id !== layer.id))} />
      ))}
    </RegistryContext.Provider>
  )
}

// A dialog or a side panel an add-on asked for. The host owns its lifecycle:
// focus, Escape, the veil and the unmount are the kit's, and the add-on only
// supplies the content.
function AddonLayer({ layer, labels, closeLabel, onClose }) {
  const { spec, kind } = layer
  const close = () => { try { spec.onClose?.() } finally { onClose() } }
  const body = (
    <AddonErrorBoundary labels={labels}>{spec.render({ close })}</AddonErrorBoundary>
  )
  if (kind === 'panel') {
    return <SideSheet title={spec.title} description={spec.description} wide={spec.wide} closeLabel={closeLabel} onClose={close} labelId={`addon-layer-${layer.id}`}>{body}</SideSheet>
  }
  return <Dialog title={spec.title} description={spec.description} closeLabel={closeLabel} onClose={close} labelId={`addon-layer-${layer.id}`}>{body}</Dialog>
}
