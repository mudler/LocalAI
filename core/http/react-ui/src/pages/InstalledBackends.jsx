import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendsApi } from '../utils/api'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import FilterBar from '../components/FilterBar'
import GalleryLoader from '../components/GalleryLoader'
import NodeDistributionChip from '../components/NodeDistributionChip'
import SplitView from '../components/split/SplitView'
import EntityRail from '../components/split/EntityRail'
import DetailHeader from '../components/split/DetailHeader'
import StatGrid from '../components/split/StatGrid'
import { stripMarkdown } from '../utils/markdown'

const STATE_GROUPS = [
  { id: 'update', labelKey: 'backends.lifecycle.updateGroup', icon: 'fa-arrow-up' },
  { id: 'installed', labelKey: 'backends.lifecycle.installedGroup', icon: 'fa-check' },
]

const VALID_STATES = new Set(['all', 'user', 'system', 'upgradable', 'offline'])

export default function InstalledBackends({
  addToast,
  catalogBackends,
  distributedEnabled,
  operations,
  upgrades,
  selectedName,
  onSelect,
}) {
  const { t } = useTranslation('admin')
  const [searchParams, setSearchParams] = useSearchParams()
  const [backends, setBackends] = useState([])
  const [loading, setLoading] = useState(true)
  const loadedOnce = useRef(false)
  const [pending, setPending] = useState(() => new Set())
  const [errors, setErrors] = useState({})
  const [globalError, setGlobalError] = useState('')
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [upgradingAll, setUpgradingAll] = useState(false)
  const [collapsedGroups, setCollapsedGroups] = useState(() => new Set())

  const query = searchParams.get('q') || ''
  const requestedState = searchParams.get('state') || 'all'
  const state = VALID_STATES.has(requestedState) ? requestedState : 'all'
  const showVariants = searchParams.get('show_all') === '1'
  const showDevelopment = searchParams.get('development') === '1'

  const updateParam = useCallback((key, value, defaultValue = '') => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev)
      if (!value || value === defaultValue) next.delete(key)
      else next.set(key, value)
      return next
    }, { replace: true })
  }, [setSearchParams])

  const fetchBackends = useCallback(async () => {
    try {
      setLoading(true)
      const data = await backendsApi.listInstalled()
      setBackends(Array.isArray(data) ? data : [])
      setGlobalError('')
    } catch (err) {
      setBackends([])
      setGlobalError(t('backends.lifecycle.loadFailed', { message: err.message }))
    } finally {
      loadedOnce.current = true
      setLoading(false)
    }
  }, [t])

  useEffect(() => { fetchBackends() }, [fetchBackends, operations.length])

  const catalogByName = new Map(catalogBackends.map(backend => [backend.name || backend.id, backend]))
  const flagsFor = (backend) => {
    const catalog = catalogByName.get(backend.Name)
    return {
      variant: !!catalog?.isAlias,
      development: !!catalog?.isDevelopment,
    }
  }

  const visibleBase = backends.filter(backend => {
    const flags = flagsFor(backend)
    if (flags.variant && !showVariants) return false
    if (flags.development && !showDevelopment) return false
    return true
  })
  const hiddenVariantCount = showVariants ? 0 : backends.filter(backend => flagsFor(backend).variant).length
  const hiddenDevelopmentCount = showDevelopment ? 0 : backends.filter(backend => flagsFor(backend).development).length
  const offlineFor = (backend) => (backend.Nodes || backend.nodes || []).some(node => {
    const status = node.node_status || node.NodeStatus
    return status && status !== 'healthy' && status !== 'draining'
  })
  const passesState = (backend) => {
    if (state === 'user') return !backend.IsSystem
    if (state === 'system') return !!backend.IsSystem
    if (state === 'upgradable') return !!upgrades[backend.Name]
    if (state === 'offline') return offlineFor(backend)
    return true
  }
  const normalizedQuery = query.trim().toLowerCase()
  const visibleBackends = visibleBase.filter(backend => passesState(backend) && (
    !normalizedQuery
    || backend.Name.toLowerCase().includes(normalizedQuery)
    || (backend.Metadata?.alias || '').toLowerCase().includes(normalizedQuery)
    || (backend.Metadata?.meta_backend_for || '').toLowerCase().includes(normalizedQuery)
  ))
  const selectedBackend = selectedName
    ? backends.find(backend => backend.Name === selectedName) || null
    : null

  const isProcessing = useCallback((name) => pending.has(name) || operations.some(operation => (
    operation.name === name && !operation.completed && !operation.error
  )), [operations, pending])

  const withPending = async (name, action) => {
    setPending(current => new Set(current).add(name))
    setErrors(current => ({ ...current, [name]: '' }))
    try {
      await action()
    } finally {
      setPending(current => {
        const next = new Set(current)
        next.delete(name)
        return next
      })
    }
  }

  const handleReinstall = async (name) => {
    try {
      await withPending(name, () => backendsApi.install(name))
      addToast(t('backends.lifecycle.reinstallStarted', { name }), 'info')
    } catch (err) {
      setErrors(current => ({
        ...current,
        [name]: t('backends.lifecycle.reinstallFailed', { message: err.message }),
      }))
    }
  }

  const handleUpgrade = async (name) => {
    try {
      await withPending(name, () => backendsApi.upgrade(name))
      addToast(t('backends.lifecycle.upgradeStarted', { name }), 'info')
    } catch (err) {
      setErrors(current => ({
        ...current,
        [name]: t('backends.lifecycle.upgradeFailed', { message: err.message }),
      }))
    }
  }

  const handleUpgradeAll = async () => {
    const names = Object.keys(upgrades)
    if (names.length === 0) return
    setUpgradingAll(true)
    setGlobalError('')
    try {
      const failures = []
      for (const name of names) {
        try {
          await backendsApi.upgrade(name)
        } catch (err) {
          failures.push(t('backends.lifecycle.upgradeAllFailed', { name, message: err.message }))
        }
      }
      if (failures.length > 0) {
        setGlobalError(failures.join(' '))
        return
      }
      addToast(t('backends.lifecycle.upgradeAllStarted', { count: names.length }), 'info')
    } finally {
      setUpgradingAll(false)
    }
  }

  const handleDelete = (name) => {
    setConfirmDialog({
      title: t('backends.lifecycle.deleteTitle'),
      message: t('backends.lifecycle.deleteMessage', { name }),
      confirmLabel: t('backends.lifecycle.delete'),
      onConfirm: async () => {
        setConfirmDialog(null)
        setErrors(current => ({ ...current, [name]: '' }))
        try {
          await backendsApi.deleteInstalled(name)
          addToast(t('backends.lifecycle.deleteSucceeded', { name }), 'success')
          onSelect(null)
          fetchBackends()
        } catch (err) {
          setErrors(current => ({
            ...current,
            [name]: t('backends.lifecycle.deleteFailed', { message: err.message }),
          }))
        }
      },
    })
  }

  const toggleGroup = (id) => {
    setCollapsedGroups(current => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const filters = [
    { key: 'all', label: t('backends.lifecycle.filterAll'), icon: 'fa-layer-group', count: visibleBase.length },
    { key: 'user', label: t('backends.lifecycle.filterUser'), icon: 'fa-download', count: visibleBase.filter(backend => !backend.IsSystem).length },
    { key: 'system', label: t('backends.lifecycle.filterSystem'), icon: 'fa-shield-alt', count: visibleBase.filter(backend => backend.IsSystem).length },
    ...(Object.keys(upgrades).length > 0 ? [{
      key: 'upgradable',
      label: t('backends.lifecycle.filterUpdates'),
      icon: 'fa-arrow-up',
      count: visibleBase.filter(backend => upgrades[backend.Name]).length,
    }] : []),
    ...(distributedEnabled && visibleBase.some(offlineFor) ? [{
      key: 'offline',
      label: t('backends.lifecycle.filterOffline'),
      icon: 'fa-exclamation-circle',
      count: visibleBase.filter(offlineFor).length,
    }] : []),
  ]

  if (loading && !loadedOnce.current) return <GalleryLoader />

  return (
    <>
      {Object.keys(upgrades).length > 0 && (
        <div className="upgrade-banner">
          <div className="upgrade-banner__text">
            <i className="fas fa-arrow-up" aria-hidden="true" />
            <span>{t('backends.lifecycle.updatesAvailable', { count: Object.keys(upgrades).length })}</span>
          </div>
          <div className="upgrade-banner__actions">
            <button className="btn btn-primary btn-sm" onClick={handleUpgradeAll} disabled={upgradingAll}>
              <i className={`fas ${upgradingAll ? 'fa-spinner fa-spin' : 'fa-arrow-up'}`} aria-hidden="true" />
              {upgradingAll ? t('backends.lifecycle.upgrading') : t('backends.lifecycle.upgradeAll')}
            </button>
          </div>
        </div>
      )}

      {globalError && (
        <div className="attention-callout attention-callout--error mb-md" role="alert">
          <i className="fas fa-circle-exclamation" aria-hidden="true" />
          <span>{globalError}</span>
        </div>
      )}

      {backends.length === 0 ? (
        <div className="empty-state empty-state--page">
          <div className="empty-state-icon"><i className="fas fa-server" /></div>
          <h2 className="empty-state-title">{t('backends.lifecycle.emptyTitle')}</h2>
          <p className="empty-state-text">{t('backends.lifecycle.emptyBody')}</p>
        </div>
      ) : (
        <>
          <FilterBar
            search={query}
            onSearchChange={value => updateParam('q', value)}
            searchPlaceholder={t('backends.lifecycle.searchPlaceholder')}
            filters={filters}
            activeFilter={state}
            onFilterChange={value => updateParam('state', value, 'all')}
            toggles={[
              {
                key: 'variants',
                label: hiddenVariantCount > 0
                  ? t('backends.lifecycle.variantsCount', { count: hiddenVariantCount })
                  : t('backends.lifecycle.variants'),
                icon: 'fa-cubes',
                checked: showVariants,
                onChange: () => updateParam('show_all', showVariants ? '' : '1'),
              },
              {
                key: 'development',
                label: hiddenDevelopmentCount > 0
                  ? t('backends.lifecycle.developmentCount', { count: hiddenDevelopmentCount })
                  : t('backends.lifecycle.development'),
                icon: 'fa-flask',
                checked: showDevelopment,
                onChange: () => updateParam('development', showDevelopment ? '' : '1'),
              },
            ]}
          />

          {visibleBackends.length === 0 ? (
            <div className="empty-state">
              <i className="fas fa-filter" />
              <p>{t('backends.lifecycle.noMatches')}</p>
              <button className="btn btn-ghost btn-sm" onClick={() => {
                setSearchParams(prev => {
                  const next = new URLSearchParams(prev)
                  next.delete('q')
                  next.delete('state')
                  return next
                }, { replace: true })
              }}>
                {t('backends.lifecycle.clearFilters')}
              </button>
            </div>
          ) : (
            <SplitView
              testId="backends-installed"
              detail={!!selectedBackend}
              rail={(
                <EntityRail
                  items={visibleBackends.map(backend => railItemForBackend(backend, upgrades, isProcessing, t))}
                  groups={STATE_GROUPS.map(group => ({ ...group, label: t(group.labelKey) }))}
                  grouped={!query.trim()}
                  collapsedGroups={collapsedGroups}
                  onToggleGroup={toggleGroup}
                  busy={loading}
                  selectedId={selectedName}
                  onSelect={onSelect}
                  countLabel={t('backends.lifecycle.countLabel', { visible: visibleBackends.length, total: backends.length })}
                  ariaLabel={t('backends.lifecycle.installedAria')}
                  testId="backends-installed-rail"
                />
              )}
              pane={selectedBackend ? (
                <InstalledBackendDetail
                  backend={selectedBackend}
                  catalog={catalogByName.get(selectedBackend.Name)}
                  upgrade={upgrades[selectedBackend.Name]}
                  processing={isProcessing(selectedBackend.Name)}
                  error={errors[selectedBackend.Name]}
                  distributedEnabled={distributedEnabled}
                  onBack={() => onSelect(null)}
                  onUpgrade={handleUpgrade}
                  onReinstall={handleReinstall}
                  onDelete={handleDelete}
                  t={t}
                />
              ) : (
                <InstalledOverview backends={backends} upgrades={upgrades} onSelect={onSelect} t={t} />
              )}
            />
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
    </>
  )
}

function railItemForBackend(backend, upgrades, isProcessing, t) {
  const version = backend.Metadata?.version || backend.Version
  const upgrade = upgrades[backend.Name]
  let groupId = 'installed'
  let stripe = 'idle'
  let meta = version ? `v${version}` : t('backends.lifecycle.installed')
  let metaTone

  if (isProcessing(backend.Name)) {
    meta = t('backends.lifecycle.working')
    metaTone = 'busy'
  } else if (upgrade) {
    groupId = 'update'
    stripe = 'err'
    meta = upgrade.available_version
      ? `v${version} → v${upgrade.available_version}`
      : t('backends.lifecycle.updateAvailable')
    metaTone = 'warn'
  }

  return { id: backend.Name, name: backend.Name, icon: 'fa-server', meta, metaTone, stripe, groupId }
}

function InstalledBackendDetail({
  backend,
  catalog,
  upgrade,
  processing,
  error,
  distributedEnabled,
  onBack,
  onUpgrade,
  onReinstall,
  onDelete,
  t,
}) {
  const name = backend.Name
  const version = backend.Metadata?.version || backend.Version
  const nodes = backend.Nodes || backend.nodes || []

  return (
    <div className="detail-pane">
      <DetailHeader
        testId="backends-installed"
        icon="fa-server"
        name={name}
        lede={catalog?.description ? stripMarkdown(catalog.description).slice(0, 220) : null}
        ledeTitle={catalog?.description ? stripMarkdown(catalog.description) : null}
        onBack={onBack}
        backLabel={t('backends.lifecycle.allBackends')}
        actions={backend.IsSystem ? (
          <span className="badge" title={t('backends.lifecycle.protectedTitle')}>
            <i className="fas fa-lock" /> {t('backends.lifecycle.protected')}
          </span>
        ) : (
          <>
            {upgrade && (
              <button className="btn btn-primary btn-sm" onClick={() => onUpgrade(name)} disabled={processing}>
                <i className="fas fa-arrow-up" />
                {upgrade.available_version
                  ? t('backends.lifecycle.upgradeTo', { version: upgrade.available_version })
                  : t('backends.lifecycle.upgrade')}
              </button>
            )}
            <ActionMenu
              ariaLabel={t('backends.lifecycle.actionsFor', { name })}
              triggerLabel={t('backends.lifecycle.actionsFor', { name })}
              items={[
                {
                  key: 'reinstall',
                  icon: 'fa-rotate',
                  label: t('backends.lifecycle.reinstall'),
                  onClick: () => onReinstall(name),
                  disabled: processing,
                },
                { divider: true },
                {
                  key: 'delete',
                  icon: 'fa-trash',
                  label: t('backends.lifecycle.deleteBackend'),
                  danger: true,
                  onClick: () => onDelete(name),
                },
              ]}
            />
          </>
        )}
      />

      {error && (
        <div className="attention-callout attention-callout--error" role="alert">
          <i className="fas fa-circle-exclamation" aria-hidden="true" />
          <span>{error}</span>
        </div>
      )}

      <StatGrid stats={[
        { label: t('backends.lifecycle.version'), value: version ? `v${version}` : '—' },
        upgrade ? {
          label: t('backends.lifecycle.available'),
          value: upgrade.available_version ? `v${upgrade.available_version}` : t('backends.lifecycle.updateAvailable'),
          tone: 'warn',
        } : null,
        {
          label: t('backends.lifecycle.managed'),
          value: backend.IsSystem ? t('backends.lifecycle.system') : t('backends.lifecycle.gallery'),
        },
      ]} />

      {distributedEnabled && nodes.length > 0 && (
        <div>
          <span className="detail-pane__label">{t('backends.lifecycle.installedOn')}</span>
          <NodeDistributionChip nodes={nodes} context="backends" compactThreshold={20} />
        </div>
      )}

      <div className="bk-detail">
        <table className="bk-detail__table">
          <tbody>
            {catalog?.description && (
              <tr>
                <td className="bk-detail__label">{t('backends.lifecycle.description')}</td>
                <td className="bk-detail__value">{stripMarkdown(catalog.description)}</td>
              </tr>
            )}
            {backend.Metadata?.uri && (
              <tr>
                <td className="bk-detail__label">{t('backends.lifecycle.source')}</td>
                <td className="bk-detail__value cell-mono wrap-anywhere">{backend.Metadata.uri}</td>
              </tr>
            )}
            {backend.Metadata?.digest && (
              <tr>
                <td className="bk-detail__label">{t('backends.lifecycle.digest')}</td>
                <td className="bk-detail__value cell-mono wrap-anywhere">{backend.Metadata.digest}</td>
              </tr>
            )}
            {backend.Metadata?.installed_at && (
              <tr>
                <td className="bk-detail__label">{t('backends.lifecycle.installedAt')}</td>
                <td className="bk-detail__value cell-mono">{backend.Metadata.installed_at}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function InstalledOverview({ backends, upgrades, onSelect, t }) {
  const staleNames = Object.keys(upgrades)
  return (
    <div className="zero-pane">
      <div className="zero-pane__hero">
        <span className="zero-pane__eyebrow">{t('backends.lifecycle.inventoryEyebrow')}</span>
        <h2 className="zero-pane__title">{t('backends.lifecycle.inventoryTitle', { count: backends.length })}</h2>
        <p className="zero-pane__text">{t('backends.lifecycle.inventoryBody')}</p>
      </div>
      <StatGrid stats={[
        { label: t('backends.lifecycle.installed'), value: backends.length },
        { label: t('backends.lifecycle.filterUpdates'), value: staleNames.length, tone: staleNames.length ? 'warn' : undefined },
        { label: t('backends.lifecycle.system'), value: backends.filter(backend => backend.IsSystem).length },
      ]} />
      {staleNames.length > 0 && (
        <div className="zero-pane__shelf">
          <div className="zero-pane__shelf-head">
            <h3 className="zero-pane__shelf-title">{t('backends.lifecycle.filterUpdates')}</h3>
          </div>
          <div className="rowlist">
            {staleNames.map(name => (
              <button className="rowline" key={name} onClick={() => onSelect(name)}>
                <span className="badge badge-warning"><i className="fas fa-arrow-up icon-tiny" /> {t('backends.lifecycle.updateAvailable')}</span>
                <span>{name}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
