/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendsApi, modelsApi, nodesApi } from '../utils/api'
import { useDebouncedCallback } from '../hooks/useDebounce'
import { useOperations } from '../hooks/useOperations'
import { useDistributedMode } from '../hooks/useDistributedMode'
import { CANCEL_UNDO_MS, useUndoableCancel } from '../hooks/useUndoableCancel'
import { backendState, opForBackend, sortInstalled, versionLabel } from '../utils/backendRows'
import { backendDependents, recommendedBackend } from '../utils/operateStatus'
import { renderMarkdown, stripMarkdown } from '../utils/markdown'
import { safeHref } from '../utils/url'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import NodeDistributionChip from '../components/NodeDistributionChip'
import NodeInstallPicker from '../components/NodeInstallPicker'
import BackendsTable from '../components/operate/BackendsTable'
import HomeUndoToast from '../components/home/HomeUndoToast'
import LoadingSpinner from '../components/LoadingSpinner'
import Icon from '../components/Icon'
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
// button that matters, and opens into everything else.
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
  const [search, setSearch] = useState(() => searchParams.get('q') || '')
  const [sortBy, setSortBy] = useState('name')
  const [sortOrder, setSortOrder] = useState('asc')
  const [page, setPage] = useState(1)
  const [showManualInstall, setShowManualInstall] = useState(false)
  const [manualUri, setManualUri] = useState('')
  const [manualName, setManualName] = useState('')
  const [manualAlias, setManualAlias] = useState('')
  const [manualError, setManualError] = useState('')
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [allBackends, setAllBackends] = useState([])
  const [installed, setInstalled] = useState([])
  const [models, setModels] = useState([])
  const [upgrades, setUpgrades] = useState({})
  const [upgradingAll, setUpgradingAll] = useState(false)
  const [checking, setChecking] = useState(false)
  const [preferDevLoaded, setPreferDevLoaded] = useState(false)
  const [preferDev, setPreferDev] = useState(false)
  const [pending, setPending] = useState(() => new Set())
  const [errors, setErrors] = useState({})
  const [globalError, setGlobalError] = useState('')
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
      // On first load, use the server's preference for development builds.
      if (!preferDevLoaded && data?.preferDevelopmentBackends) {
        setPreferDev(true)
        setPreferDevLoaded(true)
      }
    } catch (err) {
      addToast(t('backends.loadFailed', { message: err.message }), 'error')
    } finally {
      loadedOnce.current = true
      setCatalogReady(true)
      setLoading(false)
    }
  }, [activeView, search, sortBy, sortOrder, addToast, t, preferDevLoaded])

  const debouncedFetch = useDebouncedCallback(fetchBackends)
  useEffect(() => { debouncedFetch() }, [debouncedFetch, fetchBackends])

  const fetchInstalled = useCallback(async () => {
    try {
      setInstalledLoading(true)
      const data = await backendsApi.listInstalled()
      setInstalled(Array.isArray(data) ? data : [])
      setGlobalError('')
    } catch (err) {
      setInstalled([])
      setGlobalError(t('backends.installedLoadFailed', { message: err.message }))
    } finally {
      installedOnce.current = true
      setInstalledLoading(false)
    }
  }, [t])

  // Install and delete change what is installed, so the lists are read again
  // whenever the set of operations changes.
  useEffect(() => {
    if (!loading) fetchBackends()
    fetchInstalled()
    backendsApi.checkUpgrades().then(data => setUpgrades(data || {})).catch(() => {})
    modelsApi.listCapabilities().then(data => setModels(Array.isArray(data?.data) ? data.data : [])).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [operations.length])

  const catalogByName = useMemo(
    () => new Map(allBackends.map(b => [b.name || b.id, b])),
    [allBackends],
  )
  const updateNames = Object.keys(upgrades)
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
    setError(id, '')
    try {
      await nodesApi.installBackend(targetNode.id, id)
      addToast(t('backends.installingOn', { name: id, node: targetNode.name }), 'info')
      setTimeout(() => { fetchBackends(); fetchInstalled(); refetchNodes() }, 1200)
    } catch (err) {
      setError(id, t('backends.dispatchFailed', { node: targetNode.name, message: err.message }))
    }
  }

  const handleRemoveFromTarget = async (name) => {
    setError(name, '')
    try {
      await nodesApi.deleteBackend(targetNode.id, name)
      addToast(t('backends.removedFrom', { name, node: targetNode.name }), 'success')
      setTimeout(() => { fetchBackends(); fetchInstalled(); refetchNodes() }, 600)
    } catch (err) {
      setError(name, t('backends.removeFailed', { message: err.message }))
    }
  }

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

  const handleUpgrade = async (name) => {
    setError(name, '')
    markPending(name, true)
    try {
      await backendsApi.upgrade(name)
      addToast(t('backends.updateStarted', { name }), 'info')
    } catch (err) {
      setError(name, t('backends.updateFailed', { message: err.message }))
    } finally {
      markPending(name, false)
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

  const handleUpgradeAll = async () => {
    if (updateNames.length === 0) return
    setUpgradingAll(true)
    setGlobalError('')
    try {
      const failures = []
      for (const name of updateNames) {
        try {
          await backendsApi.upgrade(name)
        } catch (err) {
          failures.push(t('backends.updateAllFailed', { name, message: err.message }))
        }
      }
      if (failures.length > 0) {
        setGlobalError(failures.join(' '))
        return
      }
      addToast(t('backends.updateAllStarted', { count: updateNames.length }), 'info')
    } finally {
      setUpgradingAll(false)
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
  const handleDelete = async (name, how) => {
    let dependents = { models: [], metas: [] }
    try {
      const data = await modelsApi.listCapabilities()
      const list = Array.isArray(data?.data) ? data.data : []
      setModels(list)
      dependents = backendDependents(name, { models: list, installed })
    } catch {
      dependents = backendDependents(name, { models, installed })
    }
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
    if (catalogFilter) {
      const f = catalogFilter.toLowerCase()
      result = result.filter(b => {
        const tags = (b.tags || []).map(x => x.toLowerCase())
        return tags.some(x => x.includes(f)) || (b.name || '').toLowerCase().includes(f) || (b.description || '').toLowerCase().includes(f)
      })
    }
    return result
  }, [allBackends, showAll, showDevelopment, catalogFilter])
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
    const busy = pending.has(name)
    const isNodeScoped = Boolean(targetNode)
    const onTarget = isNodeScoped && (b.nodes || []).some(n => (n.node_id ?? n.NodeID) === targetNode.id)

    let action = null
    if (state.kind === 'installing' || state.kind === 'queued') {
      action = state.cancellable
        ? <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => cancelling.request(state.jobID)} aria-label={t('backends.cancelLabel', { name })}>{t('backends.cancel')}</button>
        : null
    } else if (state.kind === 'removing') {
      action = null
    } else if (state.kind === 'failed') {
      action = <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => handleInstall(name)} aria-label={t('backends.retryLabel', { name })}>{t('backends.retry')}</button>
    } else if (isNodeScoped) {
      action = onTarget
        ? null
        : <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="backends-install" onClick={() => handleInstallOnTarget(name)}><Icon name="download" /> {t('backends.installOnNode', { node: targetNode.name })}</button>
    } else if (b.installed) {
      action = upgrade
        ? <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={() => handleUpgrade(name)} aria-label={t('backends.updateLabel', { name })}>{t('backends.update')}</button>
        : null
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
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => handleInstallOnTarget(name)} title={t('backends.reinstallOn', { node: targetNode.name })}>
                <Icon name="refresh" /> {t('backends.reinstall')}
              </button>
              <button type="button" className="dk-btn dk-btn--danger dk-btn--sm" onClick={() => handleRemoveFromTarget(name)} title={t('backends.removeFrom', { node: targetNode.name })}>
                <Icon name="trash" /> {t('backends.remove')}
              </button>
            </>
          )}
          {!isNodeScoped && b.installed && state.kind !== 'installing' && state.kind !== 'removing' && (
            <>
              {upgrade && (
                <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={busy} onClick={() => handleUpgrade(name)} title={t('backends.updateToTitle', { version: upgrade.available_version ? `v${upgrade.available_version}` : t('backends.latest') })}>
                  <Icon name="arrow-up" /> {t('backends.update')}
                </button>
              )}
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={() => handleInstall(name)} title={t('backends.reinstall')}>
                <Icon name="refresh" /> {t('backends.reinstall')}
              </button>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm bk-remove" onClick={() => handleDelete(name, 'catalog')} title={t('backends.delete')}>
                <Icon name="trash" /> {t('backends.remove')}
              </button>
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
    const op = opForBackend(operations, name)
    const state = backendState({ installed: true, upgrade, op })
    const busy = pending.has(name) || state.kind === 'installing' || state.kind === 'queued' || state.kind === 'removing'
    const nodes = b.Nodes || b.nodes || []

    let action = null
    if (state.kind === 'installing' || state.kind === 'queued') {
      action = state.cancellable
        ? <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => cancelling.request(state.jobID)} aria-label={t('backends.cancelLabel', { name })}>{t('backends.cancel')}</button>
        : null
    } else if (b.IsSystem) {
      action = <span className="dk-badge" title={t('backends.protectedTitle')}><Icon name="lock" /> {t('backends.protected')}</span>
    } else {
      action = (
        <>
          {upgrade && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={busy} onClick={() => handleUpgrade(name)} aria-label={t('backends.updateLabel', { name })}>
              {t('backends.update')}
            </button>
          )}
          <ActionMenu
            compact
            ariaLabel={t('backends.actionsFor', { name })}
            triggerLabel={t('backends.actionsFor', { name })}
            items={[
              { key: 'reinstall', icon: 'refresh', label: t('backends.reinstallBackend'), onClick: () => handleReinstall(name), disabled: busy },
              { divider: true },
              { key: 'delete', icon: 'trash', label: t('backends.deleteBackend'), danger: true, onClick: () => handleDelete(name, 'installed') },
            ]}
          />
        </>
      )
    }

    const detail = (
      <div className="bk-detail" data-testid="backends-installed-pane">
        {errors[name] && <div className="op-error" role="alert"><Icon name="alert-circle" /> <span>{errors[name]}</span></div>}
        {catalog?.description && <p className="bk-detail__desc">{stripMarkdown(catalog.description)}</p>}
        <dl className="dk-kv bk-facts">
          <dt>{t('backends.facts.version')}</dt><dd>{versionLabel(version) || '—'}</dd>
          {upgrade && (<><dt>{t('backends.facts.available')}</dt><dd>{versionLabel(upgrade.available_version) || t('backends.state.update')}</dd></>)}
          <dt>{t('backends.facts.managed')}</dt><dd>{b.IsSystem ? t('backends.facts.system') : t('backends.facts.gallery')}</dd>
          {b.Metadata?.uri && (<><dt>{t('backends.facts.source')}</dt><dd className="bk-wrap-any">{b.Metadata.uri}</dd></>)}
          {b.Metadata?.digest && (<><dt>{t('backends.facts.digest')}</dt><dd className="bk-wrap-any">{b.Metadata.digest}</dd></>)}
          {b.Metadata?.installed_at && (<><dt>{t('backends.facts.installedAt')}</dt><dd>{b.Metadata.installed_at}</dd></>)}
        </dl>
        {distributedEnabled && nodes.length > 0 && (
          <div className="bk-onnodes">
            <span className="dk-eyebrow">{t('backends.installedOn')}</span>
            <NodeDistributionChip nodes={nodes} context="backends" compactThreshold={20} />
          </div>
        )}
        {usedBy(name)}
        <div className="bk-detail__acts">
          {!b.IsSystem && upgrade && (
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={busy} onClick={() => handleUpgrade(name)}>
              <Icon name="arrow-up" /> {upgrade.available_version ? t('backends.updateTo', { version: upgrade.available_version }) : t('backends.update')}
            </button>
          )}
          <Link className="dk-link bk-logs" to="/app/backend-logs">{t('backends.logs')}</Link>
        </div>
      </div>
    )

    return {
      name,
      description: stripMarkdown(catalog?.description || '').slice(0, 160),
      version,
      state,
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

  return (
    <div className="page page--wide op-page op-runtime" data-testid="backends">
      <h1 className="dk-sr-only">{t('backends.title')}</h1>

      <div className="bk-bar">
        <div className="dk-segmented" role="group" aria-label={t('backends.lifecycle')}>
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

        <div className="dk-input-icon bk-search">
          <Icon name="search" className="dk-icon" />
          <input
            className="dk-input"
            type="text"
            aria-label={activeView === 'installed' ? t('backends.searchInstalled') : t('backends.searchCatalog')}
            placeholder={activeView === 'installed' ? t('backends.searchInstalled') : t('backends.searchCatalog')}
            value={activeView === 'installed' ? query : search}
            onChange={e => (activeView === 'installed' ? updateUrlParam('q', e.target.value) : handleSearch(e.target.value))}
          />
        </div>

        <div className="bk-bar__acts">
          {updateNames.length > 0 && (
            <button type="button" className="dk-btn dk-btn--primary" onClick={handleUpgradeAll} disabled={upgradingAll}>
              <Icon name={upgradingAll ? 'spinner' : 'arrow-up'} spin={Boolean(upgradingAll)} />
              {upgradingAll ? t('backends.updating') : t('backends.updateAll', { count: updateNames.length })}
            </button>
          )}
          <button type="button" className="dk-btn dk-btn--ghost" onClick={handleCheck} disabled={checking}>
            <Icon name="refresh" spin={checking} /> {t('backends.check')}
          </button>
          <button type="button" className="dk-btn dk-btn--ghost" aria-expanded={showManualInstall} onClick={() => setShowManualInstall(v => !v)}>
            <Icon name={showManualInstall ? 'chevron-up' : 'plus'} /> {t('backends.fromUrl')}
          </button>
        </div>
      </div>

      <div className="bk-filters">
        {activeView === 'catalog' ? (
          <div className="bk-chips" role="group" aria-label={t('backends.filterLabel')}>
            {CATALOG_FILTERS.map(f => (
              <button
                key={f.key}
                type="button"
                className="dk-chip"
                aria-pressed={catalogFilter === f.key}
                onClick={() => { updateUrlParam('state', f.key); setPage(1) }}
              >
                {t(f.labelKey)}
              </button>
            ))}
          </div>
        ) : (
          <div className="dk-segmented bk-states" role="tablist" aria-label={t('backends.filterLabel')}>
            {installedFilters.map(f => (
              <button
                key={f.key}
                type="button"
                role="tab"
                className="dk-seg"
                aria-selected={installedState === f.key}
                onClick={() => updateUrlParam('state', f.key, 'all')}
              >
                {f.label} <span className="bk-seg__count" aria-hidden="true">{f.count}</span>
              </button>
            ))}
          </div>
        )}
        <div className="bk-chips">
          <button
            type="button"
            className="dk-chip"
            aria-pressed={showAll}
            onClick={() => { updateUrlParam('show_all', showAll ? '' : '1'); setPage(1) }}
          >
            {activeView === 'installed' && hiddenVariants > 0 ? t('backends.variantsCount', { count: hiddenVariants }) : t('backends.variants')}
          </button>
          <button
            type="button"
            className="dk-chip"
            aria-pressed={showDevelopment}
            onClick={() => { updateUrlParam('development', showDevelopment ? '0' : '1'); setPage(1) }}
          >
            {activeView === 'installed' && hiddenDevelopment > 0 ? t('backends.developmentCount', { count: hiddenDevelopment }) : t('backends.development')}
          </button>
        </div>
      </div>

      {activeView === 'catalog' && targetNode && (
        <div className="op-notice" data-testid="backends-target">
          <Icon name="target" />
          <span>{t('backends.targetBefore')} <span className="dk-mono">{targetNode.name}</span></span>
          <button className="dk-btn dk-btn--ghost dk-btn--sm" type="button" onClick={clearTarget}>
            <Icon name="close" /> {t('backends.clear')}
          </button>
        </div>
      )}

      {globalError && (
        <div className="op-error" role="alert">
          <Icon name="alert-circle" />
          <span>{globalError}</span>
        </div>
      )}

      {showManualInstall && (
        <form onSubmit={handleManualInstall} className="bk-manual dk-card">
          <h2 className="bk-manual__title">{t('backends.manualTitle')}</h2>
          <div className="bk-manual__grid">
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
            <button type="submit" className="dk-btn dk-btn--primary"><Icon name="download" /> {t('backends.install')}</button>
          </div>
          {manualError && <p className="dk-field-error" role="alert">{manualError}</p>}
        </form>
      )}

      {recommended && (
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
        <div className="op-loading"><LoadingSpinner size="lg" /></div>
      ) : activeView === 'installed' ? (
        installed.length === 0 && !globalError ? (
          <div className="dk-empty">
            <div className="dk-empty-icon"><Icon name="server" /></div>
            <h2 className="dk-empty-title">{t('backends.emptyTitle')}</h2>
            <p className="dk-empty-text">{t('backends.emptyBody')}</p>
            <Link className="dk-btn dk-btn--primary" to={hrefForView('catalog')}>{t('backends.openCatalog')}</Link>
          </div>
        ) : installedRows.length === 0 ? (
          <div className="dk-empty">
            <div className="dk-empty-icon"><Icon name="filter" /></div>
            <p className="dk-empty-text">{t('backends.noMatches')}</p>
            <button
              type="button"
              className="dk-btn dk-btn--ghost"
              onClick={() => setSearchParams(prev => {
                const next = new URLSearchParams(prev)
                next.delete('q')
                next.delete('state')
                return next
              }, { replace: true })}
            >
              {t('backends.clearFilters')}
            </button>
          </div>
        ) : (
          <BackendsTable
            rows={installedRows}
            expanded={selectedName}
            onToggle={selectBackend}
            showNodes={distributedEnabled}
            caption={t('backends.installedAria')}
          />
        )
      ) : catalogRows.length === 0 ? (
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="server" /></div>
          <h2 className="dk-empty-title">{t('backends.noneFound')}</h2>
          <p className="dk-empty-text">{search || catalogFilter ? t('backends.noneFoundFiltered') : t('backends.noneFoundBody')}</p>
        </div>
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
          <p className="bk-count">{t('backends.shown', { shown: catalogPage.length, total: allBackends.length })}</p>
          {totalPages > 1 && (
            <div className="bk-pager">
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page <= 1} aria-label={t('backends.prevPage')}>
                <Icon name="chevron-left" />
              </button>
              <span className="dk-mono">{page} / {totalPages}</span>
              <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page >= totalPages} aria-label={t('backends.nextPage')}>
                <Icon name="chevron-right" />
              </button>
            </div>
          )}
        </>
      )}

      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
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
