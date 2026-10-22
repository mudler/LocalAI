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
import { availableChatActions, navigationItems as buildNavigation, shouldPoll } from './registryState'
import { host } from './hostServices'
import { RegistryContext } from './registryContext'
import AddonErrorBoundary from './AddonErrorBoundary'
import ApprovalFrame from './ApprovalFrame'
import { actionArguments, proposalIdOf, rendererFor, resultText } from './slots'
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

function guarded(build) {
  try { return build() } catch { return [] }
}

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
  const viewerRef = useRef({ isAdmin, hasFeature })
  viewerRef.current = { isAdmin, hasFeature }
  const notifierRef = useRef(null)
  const locationsRef = useRef(new Map())
  const listeners = useRef(new Set())
  const tick = useRef(null)
  const layerSeq = useRef(0)
  const presentRef = useRef(null)
  const stateRef = useRef({ apps: [], instances: [] })

  // One stack of host-owned layers. Every layer is tied to an add-on and a
  // generation and closes with it (see the effect below).
  const pushLayer = useCallback((addonId, generation, kind, spec) => {
    const id = ++layerSeq.current
    setLayers(prev => [...prev, { id, addonId, generation, kind, spec }])
    return { close: () => setLayers(prev => prev.filter(l => l.id !== id)) }
  }, [])

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
        host: { ...host, viewer: { isAdmin: () => viewerRef.current.isAdmin === true, hasFeature: (name) => viewerRef.current.hasFeature(name) === true } },
        bindings: {
          navigate: (path, options) => navigateRef.current(path, options),
          notify: (kind, message) => notifierRef.current?.(kind, message),
          setDirty,
          current: () => locationsRef.current.get(app.id),
          openLayer: (kind, spec) => pushLayer(app.id, app.generation, kind, spec),
          presentApproval: (proposalId) => presentRef.current?.(app.id, app.generation, proposalId),
        },
      }),
    })
    return createRuntimeManager({ load, onChange: () => listeners.current.forEach(fn => fn()) })
  }, [pushLayer])

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
      // Signed out elsewhere (another tab, an expired session): nothing the
      // person read may stay drawn or cached, so every add-on is withdrawn.
      if (error && error.kind === 'auth') {
        manager.disposeAll()
        setLayers([])
        setList({ status: 'error', apps: [], error })
        return null
      }
      setList(prev => ({ status: prev.status === 'ready' ? 'ready' : 'error', apps: prev.apps, error }))
      return null
    }
  }, [api, manager])

  // Off the Add-ons pages the chain runs only while an add-on contributes UI
  // (so that its disable, crash or update is seen) and otherwise stops with no
  // timer left. Entering /app/addons or a changed feature restarts it.
  const pathname = location.pathname.replace(/\/+$/, '')
  const onAddonsPage = pathname === '/app/addons' || pathname.startsWith('/app/addons/')
  const polling = shouldPoll({ allowed, onAddonsPage, apps: list.apps, status: list.status, consumers })
  useEffect(() => {
    if (!polling || appsTabOwnsList) return undefined
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
  }, [polling, refresh, appsTabOwnsList])

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

  stateRef.current = { apps: list.apps, instances }

  // The host's approval frame for a stored proposal of an add-on. Confirm and
  // Cancel are the frame's own; the add-on's card only draws the details.
  const presentApproval = useCallback((addonId, generation, proposalId) => {
    return pushLayer(addonId, generation, 'dialog', {
      title: t('approval.title'),
      render: ({ close }) => <ApprovalFrame addonId={addonId} proposalId={proposalId} onClose={close} />,
    })
  }, [pushLayer, t])
  presentRef.current = presentApproval

  // Runs one declared chat action. A component target opens the add-on's
  // component in a host dialog. A tool target is one typed call of the add-on
  // for the tool the manifest named, through the SDK of that add-on, and the
  // answer is drawn by its declared tool-result renderer or the generic view.
  const runAction = useCallback(async (c, context) => {
    const { apps, instances: live } = stateRef.current
    const app = apps.find(a => a.id === c.addonId)
    const instance = live.find(i => i.id === c.addonId)
    if (!app || !instance || instance.status !== 'ready' || instance.generation !== c.generation) return false
    if (c.target.component) {
      const Body = c.Component
      pushLayer(c.addonId, c.generation, 'dialog', {
        title: c.label,
        render: ({ close }) => <Body {...(context || {})} close={close} />,
      })
      return true
    }
    const args = actionArguments(c, context)
    if (!args || !c.sdk) return false
    let payload
    try {
      payload = await c.sdk.api.read({ arguments: args })
    } catch {
      // The page has no notifier of its own, so the host says it in a dialog.
      pushLayer(c.addonId, c.generation, 'dialog', {
        title: c.label,
        render: () => <p role="alert" data-testid="addon-action-failed">{t('slots.actionFailed', { label: c.label })}</p>,
      })
      return false
    }
    const proposalId = proposalIdOf(payload)
    if (proposalId) {
      presentApproval(c.addonId, c.generation, proposalId)
      return true
    }
    const latest = stateRef.current
    const Renderer = rendererFor(latest.apps.find(a => a.id === c.addonId), latest.instances.find(i => i.id === c.addonId), 'tool-result', args.tool)
    pushLayer(c.addonId, c.generation, 'dialog', {
      title: c.label,
      render: () => (Renderer
        ? <Renderer result={payload} tool={args.tool} />
        : <pre className="ad-result" data-testid="addon-result">{resultText(payload)}</pre>),
    })
    return true
  }, [pushLayer, presentApproval, t])

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
    language: i18n.language,
    runAction,
    openLayer: pushLayer,
    presentApproval,
    hold: (app) => manager.hold(app),
    reload: (app) => manager.reload(app),
    setNotifier: (fn) => { notifierRef.current = fn },
    setLocation: (id, loc) => { locationsRef.current.set(id, loc) },
    // Both lists are built from add-on supplied data. A failure while building
    // them leaves the host with no entries, never with a broken render.
    navigationItems: guarded(() => buildNavigation(list.apps, { language: i18n.language })),
    chatActions: guarded(() => availableChatActions(instances, list.apps, { hasFeature: () => allowed })),
  }), [list, instances, allowed, refresh, ingest, acquire, manager, i18n.language, runAction, pushLayer, presentApproval])

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

// The content of a layer. The add-on's render runs here, as a component under
// the boundary of AddonLayer, never during the host's own render: a throw
// draws the contained error card and the host app stays.
function LayerBody({ render, close }) {
  return render({ close })
}

// A dialog or a side panel an add-on asked for. The host owns its lifecycle:
// focus, Escape, the veil and the unmount are the kit's, and the add-on only
// supplies the content.
function AddonLayer({ layer, labels, closeLabel, onClose }) {
  const { spec, kind } = layer
  const close = () => {
    try { spec.onClose?.() } catch { /* an add-on callback never blocks the host closing it */ } finally { onClose() }
  }
  const body = (
    <AddonErrorBoundary labels={labels} closeLabel={closeLabel} onClose={close}>
      <LayerBody render={spec.render} close={close} />
    </AddonErrorBoundary>
  )
  if (kind === 'panel') {
    return <SideSheet title={spec.title} description={spec.description} wide={spec.wide} closeLabel={closeLabel} onClose={close} labelId={`addon-layer-${layer.id}`}>{body}</SideSheet>
  }
  return <Dialog title={spec.title} description={spec.description} closeLabel={closeLabel} onClose={close} labelId={`addon-layer-${layer.id}`}>{body}</Dialog>
}
