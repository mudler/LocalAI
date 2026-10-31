/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import KindLibraries from '../components/catalog/KindLibraries'
import { rowInSource } from '../components/catalog/catalogState'
import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendsApi, modelsApi, nodesApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { FEATURE_BACKEND_INSTALL } from '../utils/capabilities'
import { useDebouncedCallback } from '../hooks/useDebounce'
import { useOperations } from '../hooks/useOperations'
import { useDistributedMode } from '../hooks/useDistributedMode'
import { CANCEL_UNDO_MS, useUndoableCancel } from '../hooks/useUndoableCancel'
import { backendState, libraryOrigin, opForBackend, shortDigest, sortInstalled, versionLabel } from '../utils/backendRows'
import { addonsApi } from '../utils/addonsClient'
import { backendDependents, recommendedBackend } from '../utils/operateStatus'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import { safeHref } from '../utils/url'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import NodeDistributionChip from '../components/NodeDistributionChip'
import NodeInstallPicker from '../components/NodeInstallPicker'
import BackendsTable from '../components/operate/BackendsTable'
import BackendFilters from '../components/operate/BackendFilters'
import SideSheet from '../components/SideSheet'
import { InstallSteps } from '../components/addons/AddonBits'
import HomeUndoToast from '../components/home/HomeUndoToast'
import { Button, EmptyState, LoadError, SkeletonRows } from '../components/ui'
import Icon from '../components/Icon'
import CatalogSearchBox from '../components/catalog/CatalogSearchBox'
import CatalogPager from '../components/catalog/CatalogPager'
import { BulkUpgradeTrigger, BulkUpgradeDialog } from '../components/upgrade/BulkUpgradeDialog'
import { UpdateBadge } from '../components/upgrade/UpgradeBits'
import { useBulkUpgrade } from '../hooks/useBulkUpgrade'
import './operate.css'

const CATALOG_FILTERS = [
  { key: '', labelKey: 'backends.filters.all' },
  { key: 'chat', labelKey: 'backends.filters.chat' },
  { key: 'image', labelKey: 'backends.filters.image' },
  { key: 'video', labelKey: 'backends.filters.video' },
  { key: 'tts', labelKey: 'backends.filters.tts' },
  { key: 'transcript', labelKey: 'backends.filters.stt' },
  { key: 'vision', labelKey: 'backends.filters.vision' },
]
const CATALOG_STATES = CATALOG_FILTERS.map(f => f.key)
const INSTALLED_STATES = new Set(['all', 'user', 'system', 'upgradable', 'offline'])
const ITEMS_PER_PAGE = 60

// Runtime, Backends: what can run models on this machine. One list of
// backends with two views, Installed and Catalog. A row says what the backend
// is doing now (installing with a bar, updating, current), carries the one
// button that matters and a menu for the rare ones, and opens into everything
// else.
//
// Above the list there is one row of controls (the views, search, Filters and
// a More menu) and one line of text (the count, the active filters and the
// updates). Rare actions live in More, refinements in Filters, so the list is
// what the eye lands on.
//
// What the API answers is what is shown. It returns a version and an update
// check, but no size per backend, no earlier version to roll back to and no
// dependency lookup; models name their backend, so "used by" is read from
// those.
export default function Backends() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('operate')
  const [searchParams, setSearchParams] = useSearchParams()
  const activeView = searchParams.get('view') === 'installed' ? 'installed' : 'catalog'
  const { operations, cancelOperation } = useOperations()
  const { enabled: distributedEnabled, nodes: clusterNodes, refetch: refetchNodes } = useDistributedMode()

  const [loading, setLoading] = useState(true)
  const [installedLoading, setInstalledLoading] = useState(true)
  // Variants and development builds are told apart by the catalog, so the
  // Installed list waits for it rather than flashing rows it then hides.
  const [catalogReady, setCatalogReady] = useState(false)
  const [loadError, setLoadError] = useState(null)
  const [search, setSearch] = useState(() => searchParams.get('q') || '')
  const [sortBy, setSortBy] = useState('name')
  const [sortOrder, setSortOrder] = useState('asc')
  const [page, setPage] = useState(1)
  const [showManualInstall, setShowManualInstall] = useState(false)
  // Settings links here with ?libraries=1 to land on the open panel.
  const [librariesOpen, setLibrariesOpen] = useState(() => searchParams.get('libraries') === '1')
  const [librarySources, setLibrarySources] = useState([])
  // Servers run with --disable-backend-install answer 403 to every install
  // and upgrade; the page shows that up front instead of after the click.
  const [installEnabled, setInstallEnabled] = useState(true)

  useEffect(() => {
    fetch(apiUrl('/api/features'))
      .then(r => r.json())
      .then(data => setInstallEnabled(data[FEATURE_BACKEND_INSTALL] !== false))
      .catch(() => {})
  }, [])
  const [manualUri, setManualUri] = useState('')
  const [manualName, setManualName] = useState('')
  const [manualAlias, setManualAlias] = useState('')
  const [manualError, setManualError] = useState('')
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [allBackends, setAllBackends] = useState([])
  const [librarySource, setLibrarySource] = useState(null)
  const [installed, setInstalled] = useState([])
  const [inventories, setInventories] = useState({})
  const [nodeErrors, setNodeErrors] = useState({})
  const [removalPending, setRemovalPending] = useState(false)
  const [refreshError, setRefreshError] = useState(false)
  const removalLock = useRef(null)
  // Ref guards same-tick callbacks; state also rerenders the exact target controls.
  const targetInstallLocks = useRef(new Set())
  const [targetInstalls, setTargetInstalls] = useState(() => new Set())
  const targetKey = (nodeID, name) => JSON.stringify([nodeID, name])
  const targetInstalling = (nodeID, name) => targetInstallLocks.current.has(targetKey(nodeID, name))
  const targetBusy = (nodeID, name) => rowConflict(name)
    || targetInstalls.has(targetKey(nodeID, name))
    || removalLock.current === targetKey(nodeID, name)
  const inventoryRequests = useRef({})
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; inventoryRequests.current = {} }
  }, [])
  const [models, setModels] = useState([])
  const [upgrades, setUpgrades] = useState({})
  // Newer builds of library backends, by name, and the library updates the
  // administrator started from this page: name -> { job } while one runs.
  const [libUpdates, setLibUpdates] = useState({})
  const [libWork, setLibWork] = useState({})
  const [libReload, setLibReload] = useState(0)
  const [checking, setChecking] = useState(false)
  const [preferDevLoaded, setPreferDevLoaded] = useState(false)
  const [preferDev, setPreferDev] = useState(false)
  const [pending, setPending] = useState(() => new Set())
  const [errors, setErrors] = useState({})
  const [globalError, setGlobalError] = useState('')
  const [installedError, setInstalledError] = useState(null)
  const [pickerBackend, setPickerBackend] = useState(null)
  const [pickerInitialSelection, setPickerInitialSelection] = useState([])
  // True once any catalog listing has come back. Distinguishes a cold start,
  // which has nothing to keep on screen, from a refetch, which does.
  const loadedOnce = useRef(false)
  const installedOnce = useRef(false)
  const cancelling = useUndoableCancel({ operations, cancel: cancelOperation })

  const selectedName = searchParams.get('backend')
  const targetNodeId = searchParams.get('target') || ''
  const targetNode = targetNodeId ? clusterNodes.find(n => n.id === targetNodeId) || null : null

  const requestedState = searchParams.get('state') || ''
  const catalogFilter = CATALOG_STATES.includes(requestedState) ? requestedState : ''
  const installedState = INSTALLED_STATES.has(requestedState || 'all') ? (requestedState || 'all') : 'all'
  const showAll = searchParams.get('show_all') === '1'
  // The server can ask for development builds to be preferred; the toggle then
  // starts on, once, unless the URL already says otherwise.
  const showDevelopment = searchParams.get('development') === '1' || (preferDev && !searchParams.has('development'))
  const query = searchParams.get('q') || ''

  const hrefForView = (view) => {
    const next = new URLSearchParams(searchParams)
    next.set('view', view)
    return `/app/backends?${next.toString()}`
  }

  const updateUrlParam = useCallback((key, value, defaultValue = '') => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (!value || value === defaultValue) next.delete(key)
      else next.set(key, value)
      return next
    }, { replace: true })
  }, [setSearchParams])

  // Selecting is a URL edit that keeps everything else in the query, so it
  // composes with the target-node scope rather than clobbering it.
  const selectBackend = useCallback((name) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (name && name !== prev.get('backend')) next.set('backend', name)
      else next.delete('backend')
      return next
    }, { replace: true })
  }, [setSearchParams])

  const clearTarget = useCallback(() => {
    const next = new URLSearchParams(searchParams)
    next.delete('target')
    setSearchParams(next, { replace: true })
  }, [searchParams, setSearchParams])

  // The catalog search follows the URL, so history and links restore it.
  useEffect(() => {
    if (activeView !== 'catalog') return
    setSearch(query)
  }, [activeView, query])

  const fetchBackends = useCallback(async () => {
    try {
      setLoading(true)
      const params = { page: 1, items: 9999, sort: sortBy, order: sortOrder }
      if (activeView === 'catalog' && search) params.term = search
      const data = await backendsApi.list(params)
      const list = Array.isArray(data?.backends) ? data.backends : Array.isArray(data) ? data : []
      setAllBackends(list)
      setLoadError(null)
      // On first load, use the server's preference for development builds.
      if (!preferDevLoaded && data?.preferDevelopmentBackends) {
        setPreferDev(true)
        setPreferDevLoaded(true)
      }
      return true
    } catch (err) {
      // A failed read replaces the list with the error; it never looks empty.
      setLoadError(err)
      return false
    } finally {
      loadedOnce.current = true
      setCatalogReady(true)
      setLoading(false)
    }
  }, [activeView, search, sortBy, sortOrder, preferDevLoaded])

  const debouncedFetch = useDebouncedCallback(fetchBackends)
  useEffect(() => { debouncedFetch() }, [debouncedFetch, fetchBackends])

  const fetchInstalled = useCallback(async () => {
    try {
      setInstalledLoading(true)
      const data = await backendsApi.listInstalled()
      setInstalled(Array.isArray(data) ? data : [])
      installedOnce.current = true
      setGlobalError('')
      setInstalledError(null)
      setSearchParams(prev => {
        const selected = prev.get('backend')
        if (prev.get('view') !== 'installed' || !selected || data.some(b => b.Name === selected)) return prev
        const next = new URLSearchParams(prev)
        next.delete('backend')
        return next
      }, { replace: true })
      return true
    } catch (err) {
      setGlobalError(t('backends.installedLoadFailed', { message: err.message }))
      setInstalledError(err)
      return false
    } finally {
      setInstalledLoading(false)
    }
  }, [t, setSearchParams])

  // Install and delete change what is installed, so the lists are read again
  // whenever the set of operations changes.
  useEffect(() => {
    if (!loading) fetchBackends()
    fetchInstalled()
    backendsApi.checkUpgrades().then(data => setUpgrades(data || {})).catch(() => {})
    modelsApi.listCapabilities().then(data => setModels(Array.isArray(data?.data) ? data.data : [])).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [operations.length])

  // Library backends are checked against the index their library cached. The
  // route exists only where the add-on owner runs, so a failure is no update.
  const hasLibraryBackend = installed.some(b => libraryOrigin(b))
  useEffect(() => {
    if (!hasLibraryBackend) { setLibUpdates({}); return undefined }
    const ctl = new AbortController()
    addonsApi.backends.updates(ctl.signal)
      .then(list => setLibUpdates(Object.fromEntries(list.map(u => [u.name, u]))))
      .catch(() => { if (!ctl.signal.aborted) setLibUpdates({}) })
    return () => ctl.abort()
  }, [hasLibraryBackend, installed, libReload])

  const catalogByName = useMemo(
    () => new Map(allBackends.map(b => [b.name || b.id, b])),
    [allBackends],
  )
  const updateNames = Object.keys(upgrades)
  // "Update all": each backend is updated on its own, one after another, and a
  // failure does not stop the rest. The run lives here so closing the dialog
  // keeps it going.
  const bulkItems = useMemo(() => updateNames.map(name => ({
    id: name, name, from: upgrades[name].installed_version || '', to: upgrades[name].available_version || '', startable: true,
  })), [upgrades]) // eslint-disable-line react-hooks/exhaustive-deps
  const bulk = useBulkUpgrade({
    items: bulkItems,
    runOne: async (item) => {
      try {
        await backendsApi.upgrade(item.id)
        return { ok: true }
      } catch (err) {
        return { ok: false, message: t('backends.updateAllFailed', { name: item.name, message: err.message }) }
      }
    },
  })
  const upgradingAll = bulk.running
  const installedCount = installed.length
  const recommended = activeView === 'catalog' && !search && !catalogFilter && installedOnce.current && installedCount === 0
    ? recommendedBackend(allBackends)
    : null

  const markPending = (name, on) => setPending(current => {
    const next = new Set(current)
    if (on) next.add(name)
    else next.delete(name)
    return next
  })
  const setError = (name, message) => setErrors(current => ({ ...current, [name]: message }))

  // ---- Actions ----------------------------------------------------------

  const handleInstall = async (id) => {
    setError(id, '')
    markPending(id, true)
    try {
      await backendsApi.install(id)
    } catch (err) {
      // Distributed 409 guard: surface the human message and steer the user to
      // the picker rather than failing silently. The error body has a `code`
      // of "concrete_backend_requires_target".
      const isConcreteGuard = err?.payload?.code === 'concrete_backend_requires_target'
        || (err?.message || '').includes('hardware-specific build')
      if (isConcreteGuard && distributedEnabled) {
        const b = catalogByName.get(id)
        if (b) {
          openPicker(b)
          return
        }
      }
      setError(id, t('backends.installFailed', { message: err.message }))
    } finally {
      markPending(id, false)
    }
  }

  const handleInstallOnTarget = async (id) => {
    if (!targetNode) return
    const node = { id: targetNode.id, name: targetNode.name }
    const key = targetKey(node.id, id)
    if (targetBusy(node.id, id) || targetInstalling(node.id, id)) return
    targetInstallLocks.current.add(key)
    setTargetInstalls(new Set(targetInstallLocks.current))
    setError(id, '')
    try {
      await nodesApi.installBackend(node.id, id)
      addToast(t('backends.installingOn', { name: id, node: node.name }), 'info')
      await Promise.all([loadInventory(node.id), refreshBackendData()])
    } catch (err) {
      setError(id, t('backends.dispatchFailed', { node: node.name, message: err.message }))
    } finally {
      targetInstallLocks.current.delete(key)
      if (mounted.current) setTargetInstalls(new Set(targetInstallLocks.current))
    }
  }

  // Inventory is page-local and fetched only for the expanded row's scoped nodes.
  const loadInventory = useCallback(async (nodeID) => {
    const request = Symbol(nodeID)
    inventoryRequests.current[nodeID] = request
    setInventories(prev => ({ ...prev, [nodeID]: { status: 'loading' } }))
    try {
      const data = await nodesApi.getBackends(nodeID)
      if (!Array.isArray(data)) throw new Error(t('backends.nodeRemoval.inventoryUnknown'))
      if (mounted.current && inventoryRequests.current[nodeID] === request) {
        setInventories(prev => ({ ...prev, [nodeID]: { status: 'success', data } }))
      }
      return data
    } catch (err) {
      if (mounted.current && inventoryRequests.current[nodeID] === request) {
        setInventories(prev => ({ ...prev, [nodeID]: { status: 'error', error: err.message } }))
      }
      return null
    }
  }, [t])

  const visibleRefs = activeView === 'installed'
    ? (installed.find(b => b.Name === selectedName)?.Nodes || installed.find(b => b.Name === selectedName)?.nodes || [])
    : (allBackends.find(b => (b.name || b.id) === selectedName)?.nodes || [])
  const visibleNodeIDs = distributedEnabled ? [...new Set(visibleRefs.map(n => n.node_id ?? n.NodeID))]
    .filter(id => !targetNodeId || id === targetNodeId) : []
  const inventoryScope = JSON.stringify([selectedName, activeView, visibleNodeIDs, clusterNodes])
  useEffect(() => {
    const ids = JSON.parse(inventoryScope)[2]
    ids.forEach(id => {
      const node = clusterNodes.find(n => n.id === id)
      if (node?.node_type === 'backend' && node.status === 'healthy') loadInventory(id)
    })
    return () => { inventoryRequests.current = {} }
    // Responses from a previous expanded row or node status are ignored.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inventoryScope, loadInventory])

  const nodeReason = (node, data, name) => {
    if (!node || node.node_type !== 'backend' || node.status !== 'healthy') return t('backends.nodeRemoval.unreachable')
    const entry = data?.find(b => b.name === name)
    if (!entry) return t('backends.nodeRemoval.absent')
    if (entry.is_system !== false || entry.is_meta !== false) return t('backends.nodeRemoval.protected')
    return ''
  }
  const rowConflict = (name) => {
    const state = backendState({ installed: true, op: opForBackend(operations, name) })
    return pending.has(name) || upgradingAll || ['installing', 'queued', 'removing'].includes(state.kind)
  }
  // Async confirmation must use current operation state, not the dialog's closure.
  const currentRowConflict = useRef(rowConflict)
  currentRowConflict.current = rowConflict

  const refreshBackendData = async (afterDeletion = false) => {
    const results = await Promise.all([fetchBackends(), fetchInstalled(), refetchNodes()])
    const failed = results.some(result => result === false)
    setRefreshError(previous => failed ? (afterDeletion || previous === 'deletion' ? 'deletion' : 'refresh') : false)
    return !failed
  }

  const confirmNodeRemoval = async (snapshot) => {
    if (removalLock.current || targetInstalling(snapshot.nodeID, snapshot.backendName)) return
    const key = JSON.stringify([snapshot.nodeID, snapshot.backendName])
    removalLock.current = key
    setRemovalPending(true)
    setNodeErrors(prev => ({ ...prev, [key]: '' }))
    try {
      // Refresh status too: a node can drain or disappear while the dialog is open.
      const nodes = await nodesApi.list()
      const node = nodes.find(n => n.id === snapshot.nodeID)
      const data = await loadInventory(snapshot.nodeID)
      const reason = nodeReason(node, data, snapshot.backendName)
      if (!data || reason || currentRowConflict.current(snapshot.backendName) || targetInstalling(snapshot.nodeID, snapshot.backendName)) {
        throw new Error(reason || t('backends.nodeRemoval.inventoryUnknown'))
      }
      if (!mounted.current) return
      await nodesApi.deleteBackend(snapshot.nodeID, snapshot.backendName)
      if (!mounted.current) return
      setConfirmDialog(null)
      addToast(t('backends.removedFrom', { name: snapshot.backendName, node: snapshot.nodeName }), 'success')
      const inventory = await loadInventory(snapshot.nodeID)
      const refreshed = await refreshBackendData(true)
      if (!inventory || !refreshed) setRefreshError('deletion')
    } catch (err) {
      if (!mounted.current) return
      setConfirmDialog(null)
      setNodeErrors(prev => ({ ...prev, [key]: t('backends.nodeRemoval.failed', { name: snapshot.backendName, node: snapshot.nodeName, message: err.message }) }))
      await loadInventory(snapshot.nodeID)
    } finally {
      removalLock.current = null
      if (mounted.current) setRemovalPending(false)
    }
  }

  const handleRemoveFromTarget = (snapshot) => {
    if (removalLock.current || rowConflict(snapshot.backendName) || targetInstalling(snapshot.nodeID, snapshot.backendName)) return
    setConfirmDialog({
      title: t('backends.nodeRemoval.title', { name: snapshot.backendName, node: snapshot.nodeName }),
      message: <>{t('backends.nodeRemoval.message')}{usedBy(snapshot.backendName)}</>,
      confirmLabel: t('backends.remove'),
      onConfirm: () => confirmNodeRemoval(snapshot),
    })
  }

  const nodeControls = (name, refs) => (
    <div className="stack">
      {refs.filter(ref => !targetNodeId || (ref.node_id ?? ref.NodeID) === targetNodeId).map(ref => {
        const nodeID = ref.node_id ?? ref.NodeID
        const node = clusterNodes.find(n => n.id === nodeID)
        const nodeName = node?.name || ref.node_name || ref.NodeName || nodeID
        const inventory = inventories[nodeID]
        const key = JSON.stringify([nodeID, name])
        const reason = inventory?.status === 'success'
          ? nodeReason(node, inventory.data, name)
          : t(inventory?.status === 'error' ? 'backends.nodeRemoval.inventoryError' : 'backends.nodeRemoval.inventoryUnknown')
        return (
          <div className="stack" key={nodeID}>
            <div className="hstack">
              <span className="dk-mono">{nodeName}</span>
              <span className="text-note">{reason || t('backends.nodeRemoval.managed')}</span>
              <button type="button" className="dk-btn dk-btn--danger dk-btn--sm"
                disabled={!!reason || rowConflict(name) || targetInstalling(nodeID, name) || removalPending}
                onClick={() => handleRemoveFromTarget({ nodeID, nodeName, backendName: name })}>
                <Icon name="trash" /> {t('backends.nodeRemoval.action', { node: nodeName })}
              </button>
              {inventory?.status === 'error' && <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm"
                aria-label={t('backends.nodeRemoval.retry', { node: nodeName })}
                onClick={() => loadInventory(nodeID)}>{t('backends.retry')}</button>}
            </div>
            {nodeErrors[key] && <div className="op-error" role="alert">{nodeErrors[key]}</div>}
          </div>
        )
      })}
    </div>
  )

  const openPicker = (b, initialSelection = []) => {
    setPickerBackend(b)
    setPickerInitialSelection(initialSelection)
  }

  // Healthy backend nodes that do not hold this backend yet, to pre-select in
  // the node picker.
  const missingNodesFor = (b) => {
    const have = new Set((b?.nodes || []).map(n => n.node_id ?? n.NodeID))
    return clusterNodes
      .filter(n => (!n.node_type || n.node_type === 'backend') && n.status === 'healthy' && !have.has(n.id))
      .map(n => n.id)
  }

  // An Upgrade button opens the "Update all" dialog with only its backend
  // checked; it starts nothing by itself.
  const handleUpgrade = (name) => bulk.show([name])

  // Installs the newer build the library lists. It is the administrator's own
  // action; nothing here runs on its own. The host pins the image by the digest
  // of the library entry and verifies it as for the first install.
  const handleLibraryUpdate = async (name) => {
    const update = libUpdates[name]
    if (!update) return
    setError(name, '')
    setLibWork(w => ({ ...w, [name]: { job: { phase: 'resolving' }, signed: update.signed !== false } }))
    try {
      await addonsApi.backends.install(
        { sourceId: update.sourceId, name, version: update.version }, undefined,
        { onStatus: job => mounted.current && setLibWork(w => ({ ...w, [name]: { ...w[name], job } })) },
      )
      addToast(t('backends.library.updated', { name }), 'info')
    } catch (err) {
      if (mounted.current) setError(name, t('backends.library.updateFailed', { message: err.message }))
    } finally {
      if (mounted.current) {
        setLibWork(w => { const next = { ...w }; delete next[name]; return next })
        fetchInstalled()
        setLibReload(n => n + 1)
      }
    }
  }

  const handleReinstall = async (name) => {
    setError(name, '')
    markPending(name, true)
    try {
      await backendsApi.install(name)
      addToast(t('backends.reinstallStarted', { name }), 'info')
    } catch (err) {
      setError(name, t('backends.reinstallFailed', { message: err.message }))
    } finally {
      markPending(name, false)
    }
  }

  const handleCheck = async () => {
    setChecking(true)
    try {
      const data = await backendsApi.forceCheckUpgrades()
      const found = data && typeof data === 'object' ? Object.keys(data).length : 0
      setUpgrades(data || {})
      addToast(found > 0 ? t('backends.checkFound', { count: found }) : t('backends.checkNone'), 'info')
    } catch (err) {
      addToast(t('backends.checkFailed', { message: err.message }), 'error')
    } finally {
      setChecking(false)
    }
  }

  // Removing a backend can leave a model without a runtime. The models name
  // the backend they ask for and an installed meta backend names the concrete
  // one it points at, so both are read before asking, and the dialog says what
  // would stop working.
  // Generic deletion can affect every node, so any targeted install of this
  // build conflicts, even after navigation clears the selected node.
  const deletionBlocked = (name) => removalLock.current || currentRowConflict.current(name)
    || [...targetInstallLocks.current].some(key => JSON.parse(key)[1] === name)
  const handleDelete = async (name, how) => {
    if (deletionBlocked(name)) return
    let dependents = { models: [], metas: [] }
    try {
      const data = await modelsApi.listCapabilities()
      const list = Array.isArray(data?.data) ? data.data : []
      setModels(list)
      dependents = backendDependents(name, { models: list, installed })
    } catch {
      dependents = backendDependents(name, { models, installed })
    }
    if (!mounted.current || deletionBlocked(name)) return
    setConfirmDialog({
      title: t('backends.deleteTitle'),
      message: (
        <>
          <p>{t('backends.deleteMessage', { name })}</p>
          {(dependents.models.length > 0 || dependents.metas.length > 0) && (
            <div className="bk-warning" role="note" data-testid="backend-remove-warning">
              <Icon name="warning" />
              <div>
                {dependents.models.length > 0 && (
                  <p>{t('backends.deleteModels', { count: dependents.models.length, list: dependents.models.slice(0, 6).join(', ') + (dependents.models.length > 6 ? '…' : '') })}</p>
                )}
                {dependents.metas.length > 0 && (
                  <p>{t('backends.deleteMetas', { count: dependents.metas.length, list: dependents.metas.join(', ') })}</p>
                )}
              </div>
            </div>
          )}
        </>
      ),
      confirmLabel: t('backends.delete'),
      onConfirm: async () => {
        setConfirmDialog(null)
        if (deletionBlocked(name)) return
        setError(name, '')
        try {
          if (how === 'installed') await backendsApi.deleteInstalled(name)
          else await backendsApi.delete(name)
          addToast(how === 'installed' ? t('backends.deleteSucceeded', { name }) : t('backends.deleting', { name }), how === 'installed' ? 'success' : 'info')
          if (selectedName === name) selectBackend(null)
          setTimeout(() => { fetchBackends(); fetchInstalled() }, how === 'installed' ? 0 : 1000)
        } catch (err) {
          setError(name, t('backends.deleteFailed', { message: err.message }))
        }
      },
    })
  }

  const handleManualInstall = async (e) => {
    e.preventDefault()
    setManualError('')
    if (!manualUri.trim()) { setManualError(t('backends.manualMissing')); return }
    try {
      if (targetNode) {
        // Target-node mode: route the install to the per-node endpoint, so the
        // backend lands only on this worker, not the whole cluster.
        await nodesApi.installBackend(targetNode.id, manualName.trim() || '', {
          uri: manualUri.trim(),
          name: manualName.trim() || undefined,
          alias: manualAlias.trim() || undefined,
        })
        addToast(t('backends.installingOnNode', { node: targetNode.name }), 'info')
        setTimeout(() => { fetchBackends(); fetchInstalled(); refetchNodes() }, 600)
      } else {
        const body = { uri: manualUri.trim() }
        if (manualName.trim()) body.name = manualName.trim()
        if (manualAlias.trim()) body.alias = manualAlias.trim()
        await backendsApi.installExternal(body)
      }
      setManualUri('')
      setManualName('')
      setManualAlias('')
      setShowManualInstall(false)
    } catch (err) {
      setManualError(t('backends.installFailed', { message: err.message }))
    }
  }

  const handleSearch = (value) => {
    setSearch(value)
    updateUrlParam('q', value)
    setPage(1)
  }

  const handleSort = (col) => {
    if (sortBy === col) setSortOrder(prev => (prev === 'asc' ? 'desc' : 'asc'))
    else { setSortBy(col); setSortOrder('asc') }
    setPage(1)
  }

  // ---- Catalog rows -----------------------------------------------------

  const catalogList = useMemo(() => {
    let result = allBackends
    // Concrete variants that a meta backend aliases are hidden unless "Show
    // all" is on. Standalone backends stay visible.
    if (!showAll) result = result.filter(b => b.isMeta || !b.isAlias)
    if (!showDevelopment) result = result.filter(b => !b.isDevelopment)
    if (librarySource) result = result.filter(b => rowInSource(b, librarySource))
    if (catalogFilter) {
      const f = catalogFilter.toLowerCase()
      result = result.filter(b => {
        const tags = (b.tags || []).map(x => x.toLowerCase())
        return tags.some(x => x.includes(f)) || (b.name || '').toLowerCase().includes(f) || (b.description || '').toLowerCase().includes(f)
      })
    }
    return result
  }, [allBackends, showAll, showDevelopment, catalogFilter, librarySource])
  const totalPages = Math.max(1, Math.ceil(catalogList.length / ITEMS_PER_PAGE))
  const catalogPage = catalogList.slice((page - 1) * ITEMS_PER_PAGE, page * ITEMS_PER_PAGE)

  const detailFacts = (b) => (
    <dl className="dk-kv bk-facts">
      {b.gallery && (<><dt>{t('backends.facts.repository')}</dt><dd>{typeof b.gallery === 'string' ? b.gallery : b.gallery.name || '—'}</dd></>)}
      {b.license && (<><dt>{t('backends.facts.license')}</dt><dd>{b.license}</dd></>)}
      {b.tags?.length > 0 && (
        <>
          <dt>{t('backends.facts.tags')}</dt>
          <dd className="bk-tags">{b.tags.map(tag => <span key={tag} className="dk-badge">{tag}</span>)}</dd>
        </>
      )}
      {b.urls?.length > 0 && (
        <>
          <dt>{t('backends.facts.links')}</dt>
          <dd className="bk-links">
            {b.urls.map((url, i) => (
              <a key={i} href={safeHref(url)} target="_blank" rel="noopener noreferrer" className="dk-link">
                <Icon name="external-link" /> {url}
              </a>
            ))}
          </dd>
        </>
      )}
    </dl>
  )

  const usedBy = (name) => {
    const { models: using } = backendDependents(name, { models, installed })
    if (using.length === 0) return null
    return (
      <p className="bk-usedby" data-testid="backend-used-by">
        {t('backends.usedBy', { count: using.length })}{' '}
        <span className="dk-mono">{using.slice(0, 6).join(', ')}{using.length > 6 ? '…' : ''}</span>
      </p>
    )
  }

  const catalogRows = catalogPage.map(b => {
    const name = b.name || b.id
    const op = opForBackend(operations, name, b.id)
    const upgrade = upgrades[name]
    const state = backendState({ installed: b.installed, upgrade, op })
    const busy = pending.has(name) || removalPending
    const isNodeScoped = Boolean(targetNode)
    const onTarget = isNodeScoped && (b.nodes || []).some(n => (n.node_id ?? n.NodeID) === targetNode.id)

    // An installed backend has the same menu here as in the Installed view:
    // Reinstall (which acquires, so only when installs are on) and Delete.
    const catalogMenu = b.installed && !isNodeScoped ? (
      <ActionMenu
        compact
        ariaLabel={t('backends.actionsFor', { name })}
        triggerLabel={t('backends.actionsFor', { name })}
        items={[
          ...(installEnabled ? [{ key: 'reinstall', icon: 'refresh', label: t('backends.reinstallBackend'), onClick: () => handleInstall(name), disabled: busy }, { divider: true }] : []),
          { key: 'delete', icon: 'trash', label: t('backends.deleteBackend'), danger: true, disabled: busy || !!deletionBlocked(name), onClick: () => handleDelete(name, 'catalog') },
        ]}
      />
    ) : null

    let action = null
    if (state.kind === 'installing' || state.kind === 'queued') {
      action = state.cancellable
        ? <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => cancelling.request(state.jobID)} aria-label={t('backends.cancelLabel', { name })}>{t('backends.cancel')}</button>
        : null
    } else if (state.kind === 'removing') {
      action = null
    } else if (!installEnabled) {
      action = b.installed
        ? catalogMenu
        : <span className="dk-badge" title={t('backends.installDisabledTitle')}><Icon name="lock" /> {t('backends.installDisabledShort')}</span>
    } else if (state.kind === 'failed') {
      action = <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={isNodeScoped ? targetBusy(targetNode.id, name) : busy} onClick={() => isNodeScoped ? handleInstallOnTarget(name) : handleInstall(name)} aria-label={t('backends.retryLabel', { name })}>{t('backends.retry')}</button>
    } else if (isNodeScoped) {
      action = onTarget
        ? null
        : <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="backends-install" disabled={targetBusy(targetNode.id, name)} onClick={() => handleInstallOnTarget(name)}><Icon name="download" /> {t('backends.installOnNode', { node: targetNode.name })}</button>
    } else if (b.installed) {
      action = (
        <>
          {upgrade && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={() => handleUpgrade(name)} aria-label={t('backends.updateLabel', { name })}>{t('backends.update')}</button>}
          {catalogMenu}
        </>
      )
    } else if (distributedEnabled && !b.isMeta) {
      // A hardware-specific build goes straight to the picker, because fanning
      // a CPU build out to every node is the silent footgun the guard stops.
      action = <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="backends-install" onClick={() => openPicker(b)}><Icon name="server" /> {t('backends.chooseNodes')}</button>
    } else {
      action = (
        <>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="backends-install" disabled={busy} onClick={() => handleInstall(name)} aria-label={t('backends.installLabel', { name })}>
            <Icon name="download" /> {distributedEnabled ? t('backends.installAll') : t('backends.install')}
          </button>
          {distributedEnabled && (
            <ActionMenu
              compact
              ariaLabel={t('backends.moreInstall')}
              triggerLabel={t('backends.moreInstall')}
              items={[{ key: 'nodes', icon: 'server', label: t('backends.installSpecific'), onClick: () => openPicker(b) }]}
            />
          )}
        </>
      )
    }

    const missing = distributedEnabled && !isNodeScoped ? missingNodesFor(b) : []
    const detail = (
      <div className="bk-detail">
        {errors[name] && <div className="op-error" role="alert"><Icon name="alert-circle" /> <span>{errors[name]}</span></div>}
        {b.description && (
          <div className="markdown-body bk-detail__desc" dangerouslySetInnerHTML={{ __html: renderMarkdown(b.description) }} />
        )}
        {detailFacts(b)}
        {distributedEnabled && !isNodeScoped && (
          <div className="bk-onnodes">
            <span className="dk-eyebrow">{t('backends.installedOn')}</span>
            <div className="bk-onnodes__row">
              <NodeDistributionChip nodes={b.nodes || []} />
              {missing.length > 0 && state.kind !== 'installing' && (
                <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => openPicker(b, missing)} aria-label={t('backends.installMore')}>
                  <Icon name="plus" /> {t('backends.moreNodes', { count: missing.length })}
                </button>
              )}
            </div>
          </div>
        )}
        {b.installed && usedBy(name)}
        <div className="bk-detail__acts">
          {isNodeScoped && onTarget && (
            <>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={targetBusy(targetNode.id, name)} onClick={() => handleInstallOnTarget(name)} title={t('backends.reinstallOn', { node: targetNode.name })}>
                <Icon name="refresh" /> {t('backends.reinstall')}
              </button>
              {nodeControls(name, b.nodes || [])}
            </>
          )}
          <Link className="dk-link bk-logs" to="/app/backend-logs">{t('backends.logs')}</Link>
        </div>
      </div>
    )

    return {
      name,
      description: stripMarkdown(b.description || '').slice(0, 160),
      version: b.installed ? b.version : '',
      state,
      nodes: <NodeDistributionChip nodes={b.nodes || []} />,
      action,
      detail,
    }
  })

  // ---- Installed rows ---------------------------------------------------

  const flagsFor = (b) => {
    const c = catalogByName.get(b.Name)
    return { variant: !!c?.isAlias, development: !!c?.isDevelopment }
  }
  const offlineFor = (b) => (b.Nodes || b.nodes || []).some(node => {
    const status = node.node_status || node.NodeStatus
    return status && status !== 'healthy' && status !== 'draining'
  })
  const visibleBase = installed.filter(b => {
    const f = flagsFor(b)
    if (f.variant && !showAll) return false
    if (f.development && !showDevelopment) return false
    // A library backend pins the library id; any other falls back to the
    // gallery name its catalog row carries.
    if (librarySource && libraryOrigin(b)?.id !== librarySource.id && !rowInSource(catalogByName.get(b.Name), librarySource)) return false
    return true
  })
  const hiddenVariants = showAll ? 0 : installed.filter(b => flagsFor(b).variant).length
  const hiddenDevelopment = showDevelopment ? 0 : installed.filter(b => flagsFor(b).development).length
  const normalizedQuery = query.trim().toLowerCase()
  const passesState = (b) => {
    if (installedState === 'user') return !b.IsSystem
    if (installedState === 'system') return !!b.IsSystem
    if (installedState === 'upgradable') return !!upgrades[b.Name]
    if (installedState === 'offline') return offlineFor(b)
    return true
  }
  const installedVisible = sortInstalled(visibleBase.filter(b => passesState(b) && (
    !normalizedQuery
    || b.Name.toLowerCase().includes(normalizedQuery)
    || (b.Metadata?.alias || '').toLowerCase().includes(normalizedQuery)
    || (b.Metadata?.meta_backend_for || '').toLowerCase().includes(normalizedQuery)
  )), upgrades)

  const installedRows = installedVisible.map(b => {
    const name = b.Name
    const version = b.Metadata?.version || b.Version
    const catalog = catalogByName.get(name)
    const upgrade = upgrades[name]
    const library = libraryOrigin(b)
    const libraryUpdate = library ? libUpdates[name] : null
    const work = libWork[name]
    const op = opForBackend(operations, name)
    // While a library update runs, the row shows its progress like any install.
    const state = work
      ? { kind: 'installing', progress: Math.round(work.job?.progress || 0), cancellable: false }
      : backendState({ installed: true, upgrade, op, libraryUpdate })
    const busy = pending.has(name) || removalPending || state.kind === 'installing' || state.kind === 'queued' || state.kind === 'removing'
    const nodes = b.Nodes || b.nodes || []

    let action = null
    if (state.kind === 'installing' || state.kind === 'queued') {
      action = state.cancellable
        ? <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => cancelling.request(state.jobID)} aria-label={t('backends.cancelLabel', { name })}>{t('backends.cancel')}</button>
        : null
    } else if (b.IsSystem) {
      action = <span className="dk-badge" title={t('backends.protectedTitle')}><Icon name="lock" /> {t('backends.protected')}</span>
    } else if (!installEnabled) {
      // Update and reinstall acquire; delete does not.
      action = (
        <ActionMenu
          compact
          ariaLabel={t('backends.actionsFor', { name })}
          triggerLabel={t('backends.actionsFor', { name })}
          items={[
            { key: 'delete', icon: 'trash', label: t('backends.deleteBackend'), danger: true, onClick: () => handleDelete(name, 'installed') },
          ]}
        />
      )
    } else {
      action = (
        <>
          {upgrade && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={() => handleUpgrade(name)} aria-label={t('backends.updateLabel', { name })}>
              {t('backends.update')}
            </button>
          )}
          {!upgrade && libraryUpdate && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="backend-library-update" disabled={busy} onClick={() => handleLibraryUpdate(name)} aria-label={t('backends.updateLabel', { name })}>
              {t('backends.update')}
            </button>
          )}
          <ActionMenu
            compact
            ariaLabel={t('backends.actionsFor', { name })}
            triggerLabel={t('backends.actionsFor', { name })}
            items={[
              // A library backend is not in the gallery: a gallery reinstall by name would not find it.
              ...(library ? [] : [{ key: 'reinstall', icon: 'refresh', label: t('backends.reinstallBackend'), onClick: () => handleReinstall(name), disabled: busy }, { divider: true }]),
              { key: 'delete', icon: 'trash', label: t('backends.deleteBackend'), danger: true, disabled: busy, onClick: () => handleDelete(name, 'installed') },
            ]}
          />
        </>
      )
    }

    const detail = (
      <div className="bk-detail" data-testid="backends-installed-pane">
        {errors[name] && <div className="op-error" role="alert"><Icon name="alert-circle" /> <span>{errors[name]}</span></div>}
        {work && <InstallSteps job={work.job} signed={work.signed} />}
        {catalog?.description && <p className="bk-detail__desc">{stripMarkdown(catalog.description)}</p>}
        <dl className="dk-kv bk-facts">
          <dt>{t('backends.facts.version')}</dt><dd>{versionLabel(version) || '—'}</dd>
          {upgrade && (<><dt>{t('backends.facts.available')}</dt><dd><UpdateBadge version={versionLabel(upgrade.available_version)} /></dd></>)}
          {libraryUpdate && (<><dt>{t('backends.facts.available')}</dt><dd data-testid="library-update-facts">{versionLabel(libraryUpdate.version)} <span className="dk-mono">{shortDigest(libraryUpdate.digest)}</span></dd></>)}
          <dt>{t('backends.facts.managed')}</dt><dd>{b.IsSystem ? t('backends.facts.system') : library ? t('backends.facts.library') : t('backends.facts.gallery')}</dd>
          {library && (<><dt>{t('backends.facts.library')}</dt><dd className="dk-mono" data-testid="library-id">{library.id}</dd></>)}
          {b.Metadata?.uri && (<><dt>{t('backends.facts.source')}</dt><dd className="bk-wrap-any">{b.Metadata.uri}</dd></>)}
          {b.Metadata?.digest && (<><dt>{library ? t('backends.facts.pinnedDigest') : t('backends.facts.digest')}</dt><dd className="bk-wrap-any">{b.Metadata.digest}</dd></>)}
          {b.Metadata?.installed_at && (<><dt>{t('backends.facts.installedAt')}</dt><dd>{b.Metadata.installed_at}</dd></>)}
        </dl>
        {distributedEnabled && nodes.length > 0 && (
          <div className="bk-onnodes">
            <span className="dk-eyebrow">{t('backends.installedOn')}</span>
            <NodeDistributionChip nodes={nodes} context="backends" compactThreshold={20} />
            {nodeControls(name, nodes)}
          </div>
        )}
        {usedBy(name)}
        <div className="bk-detail__acts">
          <Link className="dk-link bk-logs" to="/app/backend-logs">{t('backends.logs')}</Link>
        </div>
      </div>
    )

    return {
      name,
      description: stripMarkdown(catalog?.description || '').slice(0, 160),
      version,
      state,
      library,
      nodes: <NodeDistributionChip nodes={nodes} compactThreshold={2} />,
      action,
      detail,
    }
  })

  const installedFilters = [
    { key: 'all', label: t('backends.installedFilters.all'), count: visibleBase.length },
    { key: 'user', label: t('backends.installedFilters.user'), count: visibleBase.filter(b => !b.IsSystem).length },
    { key: 'system', label: t('backends.installedFilters.system'), count: visibleBase.filter(b => b.IsSystem).length },
    ...(updateNames.length > 0 ? [{ key: 'upgradable', label: t('backends.installedFilters.updates'), count: visibleBase.filter(b => upgrades[b.Name]).length }] : []),
    ...(distributedEnabled && visibleBase.some(offlineFor) ? [{ key: 'offline', label: t('backends.installedFilters.offline'), count: visibleBase.filter(offlineFor).length }] : []),
  ]

  const showCatalogCold = activeView === 'catalog' && loading && !loadedOnce.current
  const showInstalledCold = activeView === 'installed' && ((installedLoading && !installedOnce.current) || !catalogReady)

  // ---- Toolbar ----------------------------------------------------------

  const installedLoadFailed = activeView === 'installed' && installed.length === 0 && !!installedError
  // Nothing installed: the empty state says what to do, and there is nothing
  // to search or filter.
  const installedEmpty = activeView === 'installed' && installedOnce.current && installed.length === 0 && !installedError
  const noList = installedEmpty || installedLoadFailed

  const STATE_LABELS = {
    user: t('backends.installedFilters.user'),
    system: t('backends.installedFilters.system'),
    upgradable: t('backends.installedFilters.updates'),
    offline: t('backends.installedFilters.offline'),
  }
  const libraryGroup = librarySources.length > 0 ? {
    id: 'library',
    legend: t('backends.filterGroups.library'),
    kind: 'radio',
    value: librarySource?.id || '',
    onChange: id => { setLibrarySource(librarySources.find(src => src.id === id) || null); setPage(1) },
    options: [{ value: '', label: t('backends.allLibraries') }, ...librarySources.map(src => ({ value: src.id, label: src.name }))],
  } : null
  const filterGroups = [
    activeView === 'catalog'
      ? {
        id: 'type',
        legend: t('backends.filterGroups.type'),
        kind: 'radio',
        value: catalogFilter,
        onChange: key => { updateUrlParam('state', key); setPage(1) },
        options: CATALOG_FILTERS.map(f => ({ value: f.key, label: t(f.labelKey) })),
      }
      : {
        id: 'state',
        legend: t('backends.filterGroups.state'),
        kind: 'radio',
        value: installedState,
        onChange: key => updateUrlParam('state', key, 'all'),
        options: installedFilters.map(f => ({ value: f.key, label: f.label, count: f.count })),
      },
    {
      id: 'show',
      legend: t('backends.filterGroups.show'),
      kind: 'check',
      options: [
        {
          key: 'variants',
          label: t('backends.showVariants'),
          count: activeView === 'installed' && hiddenVariants > 0 ? hiddenVariants : undefined,
          checked: showAll,
          onChange: on => { updateUrlParam('show_all', on ? '1' : ''); setPage(1) },
        },
        {
          key: 'development',
          label: t('backends.showDevelopment'),
          count: activeView === 'installed' && hiddenDevelopment > 0 ? hiddenDevelopment : undefined,
          checked: showDevelopment,
          onChange: on => { updateUrlParam('development', on ? '1' : '0'); setPage(1) },
        },
      ],
    },
    libraryGroup,
  ].filter(Boolean)

  // Every refinement that differs from the default, as a chip that removes it.
  const activeFilters = []
  if (activeView === 'catalog' && catalogFilter) {
    const f = CATALOG_FILTERS.find(x => x.key === catalogFilter)
    activeFilters.push({ key: 'type', label: t(f.labelKey), clear: () => { updateUrlParam('state', ''); setPage(1) } })
  }
  if (activeView === 'installed' && installedState !== 'all') {
    activeFilters.push({ key: 'state', label: STATE_LABELS[installedState] || installedState, clear: () => updateUrlParam('state', '') })
  }
  if (showAll) activeFilters.push({ key: 'variants', label: t('backends.showVariants'), clear: () => { updateUrlParam('show_all', ''); setPage(1) } })
  if (showDevelopment) activeFilters.push({ key: 'development', label: t('backends.showDevelopment'), clear: () => { updateUrlParam('development', '0'); setPage(1) } })
  if (librarySource) activeFilters.push({ key: 'library', label: librarySource.name, clear: () => { setLibrarySource(null); setPage(1) } })

  // Rare actions. The opener gets focus first, so a sheet opened from the menu
  // returns focus to it when it closes.
  const moreItems = [
    ...(installEnabled ? [
      { key: 'check', icon: 'refresh', label: t('backends.check'), disabled: checking, onClick: handleCheck },
      { key: 'url', icon: 'plus', label: t('backends.installFromUrl'), onClick: (trigger) => { trigger?.focus(); setShowManualInstall(true) } },
      { divider: true },
    ] : []),
    { key: 'libraries', icon: 'store', label: t('backends.manageLibraries'), onClick: (trigger) => { trigger?.focus(); setLibrariesOpen(true) } },
  ]

  const listReady = activeView === 'installed'
    ? !showInstalledCold && installed.length > 0
    : !showCatalogCold && allBackends.length > 0
  const showCount = listReady
  const shownCount = activeView === 'installed' ? installedVisible.length : catalogList.length
  const totalCount = activeView === 'installed' ? installed.length : allBackends.length
  const showUpdates = installEnabled && (updateNames.length > 0 || Boolean(bulk.run))

  return (
    <div className="page page--wide op-page op-runtime" data-testid="backends">
      <h1 className="dk-sr-only">{t('backends.title')}</h1>

      <KindLibraries
        kind="backend"
        bare
        onSelect={setLibrarySource}
        onSources={setLibrarySources}
        panelOpen={librariesOpen}
        onPanelOpen={setLibrariesOpen}
      />

      {/* One row of controls and one line of text, kept in reach on a long list. */}
      <div className="bk-head" data-testid="backends-toolbar">
        <div className="bk-bar">
          <div className="dk-segmented bk-views" role="group" aria-label={t('backends.lifecycle')}>
            <Link
              className="dk-seg bk-seg"
              to={hrefForView('installed')}
              aria-current={activeView === 'installed' ? 'page' : undefined}
            >
              {t('backends.installedView')} <span className="bk-seg__count" aria-hidden="true">{installedCount}</span>
            </Link>
            <Link
              className="dk-seg bk-seg"
              to={hrefForView('catalog')}
              aria-current={activeView === 'catalog' ? 'page' : undefined}
            >
              {t('backends.catalogView')} <span className="bk-seg__count" aria-hidden="true">{allBackends.length}</span>
            </Link>
          </div>

          {!noList && (
            <CatalogSearchBox
              className="bk-search"
              label={activeView === 'installed' ? t('backends.searchInstalled') : t('backends.searchCatalog')}
              value={activeView === 'installed' ? query : search}
              onChange={value => (activeView === 'installed' ? updateUrlParam('q', value) : handleSearch(value))}
            />
          )}

          {!noList && (
            <BackendFilters
              groups={filterGroups}
              active={activeFilters.length}
              testId="backends-filters"
              labels={{ button: t('backends.filtersButton'), title: t('backends.filterLabel'), done: t('backends.filtersDone') }}
            />
          )}

          <span className="bk-more">
            <ActionMenu
              items={moreItems}
              ariaLabel={t('backends.moreActions')}
              triggerLabel={t('backends.moreActions')}
            />
          </span>
        </div>

        {(showCount || activeFilters.length > 0 || checking || showUpdates) && (
          <div className="bk-status">
            {showCount && (
              <span className="bk-count" aria-live="polite" data-testid="backends-count">
                {shownCount === totalCount
                  ? t('backends.count', { count: totalCount })
                  : t('backends.countOf', { shown: shownCount, total: totalCount })}
              </span>
            )}
            {activeFilters.length > 0 && (
              <ul className="bk-active" aria-label={t('backends.activeFilters')}>
                {activeFilters.map(f => (
                  <li key={f.key} className="dk-chip bk-active__chip">
                    <span>{f.label}</span>
                    <button
                      type="button"
                      className="bk-active__remove"
                      aria-label={t('backends.removeFilter', { name: f.label })}
                      onClick={f.clear}
                    >
                      <Icon name="close" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {checking && (
              <span className="bk-status__note" role="status">
                <Icon name="spinner" spin /> {t('backends.checking')}
              </span>
            )}
            {showUpdates && (
              <span className="bk-updates" data-testid="backends-updates">
                {updateNames.length > 0 && (
                  <span className="bk-updates__text">
                    <Icon name="arrow-up" /> {t('backends.updatesAvailable', { count: updateNames.length })}
                  </span>
                )}
                <BulkUpgradeTrigger
                  bulk={bulk}
                  className="dk-btn dk-btn--ghost dk-btn--sm"
                  quiet
                  labels={{ trigger: t('backends.reviewUpdates') }}
                />
              </span>
            )}
          </div>
        )}
      </div>

      {!installEnabled && (
        <div className="op-notice" data-testid="backends-install-disabled">
          <Icon name="lock" />
          <span>{t('backends.installDisabled')}</span>
        </div>
      )}

      {activeView === 'catalog' && targetNode && (
        <div className="op-notice" data-testid="backends-target">
          <Icon name="target" />
          <span>{t('backends.targetBefore')} <span className="dk-mono">{targetNode.name}</span></span>
          <button className="dk-btn dk-btn--ghost dk-btn--sm" type="button" onClick={clearTarget}>
            <Icon name="close" /> {t('backends.clear')}
          </button>
        </div>
      )}

      {(refreshError || (globalError && !installedLoadFailed)) && (
        <div className="op-error" role="alert">
          <span>{refreshError ? t(refreshError === 'deletion' ? 'backends.nodeRemoval.stale' : 'backends.nodeRemoval.refreshFailed') : globalError}</span>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" aria-label={t('backends.nodeRemoval.refresh')} onClick={() => refreshBackendData()}>{t('backends.retry')}</button>
        </div>
      )}
      {showManualInstall && (
        <SideSheet
          title={t('backends.manualTitle')}
          onClose={() => setShowManualInstall(false)}
          closeLabel={t('backends.close')}
          labelId="bk-manual-title"
          testId="backends-manual"
          footer={(
            <>
              <Button variant="ghost" onClick={() => setShowManualInstall(false)}>{t('backends.cancel')}</Button>
              <Button type="submit" form="bk-manual-form" variant="primary" icon="download">{t('backends.install')}</Button>
            </>
          )}
        >
          <form id="bk-manual-form" onSubmit={handleManualInstall} className="bk-manual">
            <div className="dk-field">
              <label className="dk-label" htmlFor="bk-uri">{t('backends.manualUri')}</label>
              <input id="bk-uri" className="dk-input dk-input--mono" value={manualUri} onChange={e => setManualUri(e.target.value)} placeholder="oci://quay.io/example/backend:latest" />
            </div>
            <div className="dk-field">
              <label className="dk-label" htmlFor="bk-name">{t('backends.manualName')}</label>
              <input id="bk-name" className="dk-input" value={manualName} onChange={e => setManualName(e.target.value)} placeholder="my-backend" />
            </div>
            <div className="dk-field">
              <label className="dk-label" htmlFor="bk-alias">{t('backends.manualAlias')}</label>
              <input id="bk-alias" className="dk-input" value={manualAlias} onChange={e => setManualAlias(e.target.value)} placeholder="alias" />
            </div>
            {manualError && <p className="dk-field-error" role="alert">{manualError}</p>}
          </form>
        </SideSheet>
      )}

      {installEnabled && recommended && (
        <div className="bk-recommend" data-testid="backends-recommend">
          <div>
            <h2 className="bk-recommend__title">{t('backends.recommendTitle', { name: recommended.name || recommended.id })}</h2>
            <p>{t('backends.recommendBody')}</p>
          </div>
          <button type="button" className="dk-btn dk-btn--primary" onClick={() => handleInstall(recommended.name || recommended.id)}>
            <Icon name="download" /> {t('backends.installNamed', { name: recommended.name || recommended.id })}
          </button>
        </div>
      )}

      {showCatalogCold || showInstalledCold ? (
        <SkeletonRows />
      ) : activeView === 'installed' ? (
        installedLoadFailed ? (
          <LoadError title={t('backends.installedLoadFailedTitle')} error={installedError} onRetry={fetchInstalled} />
        ) : installed.length === 0 ? (
          <EmptyState
            icon="server"
            title={t('backends.emptyTitle')}
            body={t('backends.emptyBody')}
            actions={<Button variant="primary" to={hrefForView('catalog')}>{t('backends.openCatalog')}</Button>}
          />
        ) : installedRows.length === 0 ? (
          <EmptyState
            variant="no-results"
            body={librarySource ? t('backends.libraryEmpty', { name: librarySource.name }) : t('backends.noMatches')}
            actions={(
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                // Every filter goes, the library one too: it is what the
                // empty state names when it is set.
                setLibrarySource(null)
                setPage(1)
                setSearchParams(prev => {
                  const next = new URLSearchParams(prev)
                  next.delete('q')
                  next.delete('state')
                  return next
                }, { replace: true })
              }}
              data-testid="backends-clear-filters"
            >
              {t('backends.clearFilters')}
            </Button>
            )}
          />
        ) : (
          <BackendsTable
            rows={installedRows}
            expanded={selectedName}
            onToggle={selectBackend}
            showNodes={distributedEnabled}
            caption={t('backends.installedAria')}
          />
        )
      ) : activeView === 'catalog' && loadError && allBackends.length === 0 ? (
        <LoadError title={t('backends.loadFailedTitle')} error={loadError} onRetry={fetchBackends} />
      ) : catalogRows.length === 0 ? (
        <EmptyState
          variant={search || catalogFilter || librarySource ? 'no-results' : 'first-run'}
          icon="server"
          title={t('backends.noneFound')}
          body={librarySource ? t('backends.libraryEmpty', { name: librarySource.name }) : search || catalogFilter ? t('backends.noneFoundFiltered') : t('backends.noneFoundBody')}
        />
      ) : (
        <>
          <BackendsTable
            rows={catalogRows}
            expanded={selectedName}
            onToggle={selectBackend}
            showNodes={distributedEnabled}
            sort={sortBy}
            order={sortOrder}
            onSort={handleSort}
            busy={loading}
            caption={t('backends.catalogAria')}
          />
          <CatalogPager
            className="bk-pager"
            page={page}
            totalPages={totalPages}
            onPage={setPage}
            labels={{ previous: t('backends.prevPage'), next: t('backends.nextPage') }}
          />
        </>
      )}

      <BulkUpgradeDialog bulk={bulk} labels={{ title: t('backends.updateAllTitle'), intro: t('backends.updateAllIntro'), action: t('backends.updateSelected') }} />
      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger
        pending={removalPending}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => { if (!removalLock.current) setConfirmDialog(null) }}
      />

      <NodeInstallPicker
        open={!!pickerBackend}
        onClose={() => { setPickerBackend(null); setPickerInitialSelection([]) }}
        onComplete={() => { fetchBackends(); fetchInstalled(); refetchNodes() }}
        backend={pickerBackend}
        nodes={clusterNodes}
        allBackends={allBackends}
        installedNodeIds={(pickerBackend?.nodes || []).map(n => n.node_id ?? n.NodeID)}
        initialSelection={pickerInitialSelection}
        addToast={addToast}
      />

      {cancelling.waitingFor && (
        <HomeUndoToast
          key={cancelling.waitingFor.jobID}
          message={t('activity.cancellingToast', { name: cancelling.waitingFor.name })}
          undoLabel={t('activity.undo')}
          dismissLabel={t('activity.cancelNow')}
          duration={CANCEL_UNDO_MS}
          testId="backends-undo-toast"
          onUndo={cancelling.undo}
          onExpire={cancelling.commit}
        />
      )}
    </div>
  )
}
