import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../ConfirmDialog'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import SideSheet from '../SideSheet'
import { useModels } from '../../hooks/useModels'
import { useGalleryEnrichment } from '../../hooks/useGalleryEnrichment'
import { useModelSizes } from '../../hooks/useModelSizes'
import { useModelStorage } from '../../hooks/useModelStorage'
import { useCleanupFacts } from '../../hooks/useCleanupFacts'
import { useBuildDuplicates } from '../../hooks/useBuildDuplicates'
import { systemApi } from '../../utils/api'
import { buildCleanupPlan, totalSize, UNDO_MS } from '../../utils/cleanupPlan'
import { diskEntry } from '../../utils/modelStorage'
import { gbLabel } from '../../utils/modelLedger'
import { Button, EmptyState, Tag } from '../ui'

const TIERS = ['safe', 'probably', 'call']

// Which models are running right now. The capabilities list says where a model
// is loaded in a cluster; /system says what this host has loaded.
function useRunning(enabled, refreshToken) {
  const [ids, setIds] = useState(() => new Set())
  const [ready, setReady] = useState(false)
  const read = useCallback(async () => {
    try {
      const info = await systemApi.info()
      setIds(new Set((Array.isArray(info?.loaded_models) ? info.loaded_models : []).map(m => m.id)))
    } catch {
      setIds(new Set())
    } finally {
      setReady(true)
    }
  }, [])
  useEffect(() => { if (enabled) read() }, [enabled, refreshToken, read])
  return { ids, ready, read }
}

function refText(t, ref) {
  return t(`cleanup.ref.${ref.kind}`, { name: ref.name })
}

// What a model gives back when removed alone: its own files. Zero is a real
// answer when every file is shared, and reads as zero, not as "unknown".
function sizeText(t, item) {
  if (item.sizeSource === 'disk') return gbLabel(item.size)
  return item.size ? gbLabel(item.size) : t('cleanup.sizeUnknown')
}

// The line under a model's reason: files it shares with other installed models
// (they stay on disk), and files its config names that are gone.
// eslint-disable-next-line no-unused-vars
function FactLines({ t, item }) {
  if (item.sharedWith.length === 0 && item.missing.length === 0) return null
  return (
    <>
      {item.sharedWith.length > 0 && (
        <span className="ledger-cr__why" data-testid="cleanup-shared">
          {t('cleanup.sharedWith', {
            size: gbLabel(item.sharedWith.reduce((n, s) => n + s.bytes, 0)),
            models: item.sharedWith.map(s => s.model).join(', '),
          })}
        </span>
      )}
      {item.missing.length > 0 && (
        <span className="ledger-cr__why ledger-cr__why--warn" data-testid="cleanup-missing-item">
          {t('cleanup.missing.item', { count: item.missing.length })}
        </span>
      )}
    </>
  )
}

function reasonText(t, reason) {
  const base = t(`cleanup.reason.${reason.key}`, { keep: reason.keep })
  return reason.unverified ? `${base} ${t('cleanup.reason.unverified')}` : base
}

function protectedText(t, reasons) {
  return reasons.map(r => (
    r.key === 'usedBy'
      ? t('cleanup.reason.usedBy', { by: r.refs.map(ref => refText(t, ref)).join(', ') })
      : t(`cleanup.reason.${r.key}`)
  )).join(' ')
}

// The review sheet. It opens from the disk strip and ranks installed models by
// how safe each is to remove, using only what the API reports. It owns its data
// so nothing is fetched until it is open.
//
//   disk            diskState() of the models volume
//   hiddenIds       models waiting in an undo window, which no longer count
//   removalPending  a batch is waiting or being removed
//   refreshToken    changes when the installed list changed under the page
//   onRemove(items) hand the confirmed batch to the page, which owns the undo
export default function CleanupSheet(props) {
  // Mounted only while open, so nothing is fetched, and no selection lingers,
  // until the user asks for the review.
  if (!props.open) return null
  return <Sheet {...props} />
}

function Sheet({ onClose, disk, hiddenIds, removalPending, refreshToken, onRemove }) {
  const open = true
  const { t } = useTranslation('models')
  const { models, loading: modelsLoading, error: modelsError, refetch } = useModels()
  const { enrichModel, loaded: galleryLoaded } = useGalleryEnrichment(open)
  const running = useRunning(open, refreshToken)
  const facts = useCleanupFacts(open)
  const storage = useModelStorage(open, refreshToken)
  const [selected, setSelected] = useState(() => new Set())
  const [confirm, setConfirm] = useState(null)
  const [checking, setChecking] = useState(false)
  const [showProtected, setShowProtected] = useState(false)

  useEffect(() => { if (open) refetch() }, [open, refreshToken, refetch])

  const isRunning = useCallback(model => (
    !model.disabled && (running.ids.has(model.id) || (Array.isArray(model.loaded_on) && model.loaded_on.length > 0))
  ), [running.ids])

  const visible = useMemo(
    () => models.filter(m => !hiddenIds.has(m.id)),
    [models, hiddenIds],
  )
  const ids = useMemo(() => visible.map(m => m.id), [visible])
  const galleryIds = useMemo(
    () => new Set(ids.filter(id => enrichModel(id))),
    [ids, enrichModel],
  )
  // The gallery's estimate is only asked for a model the on-disk report has
  // nothing on, and only after that report has answered.
  const estimable = storage.status === 'loading'
    ? []
    : ids.filter(id => galleryIds.has(id) && !diskEntry(storage.index, id))
  const sizes = useModelSizes(estimable, open)
  const { duplicates, loading: dupLoading } = useBuildDuplicates(
    ids,
    id => !!enrichModel(id)?.has_variants,
    open && galleryLoaded,
  )

  const plan = useMemo(() => buildCleanupPlan({
    models: visible.map(m => ({
      id: m.id,
      backend: m.backend,
      disabled: !!m.disabled,
      pinned: !!m.pinned,
      source: m.source,
      running: isRunning(m),
    })),
    sizes,
    storage: storage.index,
    references: facts.references,
    duplicates,
    galleryIds,
    verified: facts.verified,
  }), [visible, sizes, storage.index, facts.references, facts.verified, duplicates, galleryIds, isRunning])

  const ready = !modelsLoading && running.ready && facts.loaded && galleryLoaded && storage.status !== 'loading'
  const byId = useMemo(() => {
    const map = new Map()
    for (const tier of TIERS) for (const item of plan.groups[tier]) map.set(item.id, { ...item, tier })
    return map
  }, [plan])

  // A selection never outlives its row: a model that became protected, or left
  // the list, drops out of it.
  useEffect(() => {
    setSelected(prev => {
      const next = new Set([...prev].filter(id => byId.has(id)))
      return next.size === prev.size ? prev : next
    })
  }, [byId])

  const toggle = (id) => setSelected(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })
  const toggleTier = (tier) => setSelected(prev => {
    const items = plan.groups[tier]
    const all = items.every(item => prev.has(item.id))
    const next = new Set(prev)
    for (const item of items) {
      if (all) next.delete(item.id)
      else next.add(item.id)
    }
    return next
  })

  const chosen = [...selected].map(id => byId.get(id)).filter(Boolean)
  const frees = totalSize(chosen, storage.index)
  const afterFree = disk ? disk.free + frees.bytes : null
  const reclaimable = totalSize(TIERS.flatMap(tier => plan.groups[tier]), storage.index)
  // Models whose config names files that are not on disk, safe to remove or not.
  const brokenModels = [...TIERS.flatMap(tier => plan.groups[tier]), ...plan.protected]
    .filter(item => item.missing.length > 0)
    .map(item => item.id)
  const modelCount = TIERS.reduce((n, tier) => n + plan.groups[tier].length, 0)

  // The dry run: look again before asking. What was safe a minute ago may have
  // been loaded or named by an agent since.
  const review = async () => {
    setChecking(true)
    const [next] = await Promise.all([facts.refresh(), running.read()])
    setChecking(false)
    // The picked items are kept as they were chosen: what the look finds in use
    // is reported against them, not silently dropped from the selection.
    setConfirm({ recheck: next, picked: chosen })
  }

  const confirmed = (() => {
    if (!confirm) return null
    // Re-plan against the fresh references. Anything now protected is dropped
    // from the batch, and the dialog says so.
    const fresh = confirm.recheck
    const refs = fresh?.references || facts.references
    const stillOk = []
    const dropped = []
    for (const item of confirm.picked) {
      const model = visible.find(m => m.id === item.id)
      if ((refs.get(item.id) || []).length > 0 || (model && isRunning(model))) dropped.push(item)
      else stillOk.push(item)
    }
    return { stillOk, dropped }
  })()

  const nothingLeft = !!confirmed && confirmed.stillOk.length === 0
  const confirmedFrees = confirmed ? totalSize(confirmed.stillOk, storage.index) : { bytes: 0, unknown: 0 }
  const confirmBody = confirmed && (nothingLeft ? (
    <div className="ledger-confirm">
      <p className="ledger-confirm__warn" role="status">
        {t('cleanup.confirm.dropped', { names: confirmed.dropped.map(i => i.id).join(', ') })}
      </p>
    </div>
  ) : (
    <div className="ledger-confirm">
      <p>
        {afterFree != null
          ? t('cleanup.confirm.effect', { freed: gbLabel(confirmedFrees.bytes), after: gbLabel(disk.free + confirmedFrees.bytes), now: gbLabel(disk.free) })
          : t('cleanup.confirm.effectNoDisk', { freed: gbLabel(confirmedFrees.bytes) })}
        {confirmedFrees.unknown > 0 && ` ${t('cleanup.confirm.unknownSizes', { count: confirmedFrees.unknown })}`}
      </p>
      <table className="ledger-confirm__table" data-testid="cleanup-dry-run">
        <caption className="dk-sr-only">{t('cleanup.confirm.caption')}</caption>
        <tbody>
          {confirmed.stillOk.map(item => (
            <tr key={item.id}>
              <th scope="row">{item.id}</th>
              <td>{reasonText(t, item.reason)}</td>
              <td className="dk-num">{sizeText(t, item)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {confirmed.dropped.length > 0 && (
        <p className="ledger-confirm__warn" role="status">
          {t('cleanup.confirm.dropped', { names: confirmed.dropped.map(i => i.id).join(', ') })}
        </p>
      )}
      <p className="ledger-confirm__ok">
        <Icon name="check-circle" /> {t('cleanup.confirm.checked')}
      </p>
      <p className="dk-hint">{t('cleanup.confirm.hold', { seconds: Math.round(UNDO_MS / 1000) })}</p>
    </div>
  ))

  const doRemove = () => {
    const batch = confirmed.stillOk
    setConfirm(null)
    if (batch.length === 0) return
    onRemove(batch)
    setSelected(new Set())
  }

  const pct = (bytes) => (disk && disk.total > 0 ? bytes / disk.total : 0)
  const freedFraction = disk ? Math.min(pct(frees.bytes), pct(disk.models)) : 0

  return (
    <>
    <SideSheet
      title={t('cleanup.title')}
      description={disk ? t('cleanup.desc', { free: gbLabel(disk.free), total: gbLabel(disk.total), models: gbLabel(disk.models) }) : null}
      closeLabel={t('cleanup.close')}
      labelId="cleanup-title"
      testId="cleanup-sheet"
      initialFocus="close"
      veilClass="ledger-veil"
      bodyClass="ledger-sheet__body"
      bodyProps={{ 'aria-busy': !ready }}
      footClass="ledger-effect"
      footProps={{ 'data-testid': 'cleanup-effect', 'data-empty': chosen.length === 0 ? 'true' : 'false' }}
      onClose={onClose}
      footer={(
        <>
          <div className="ledger-effect__text" aria-live="polite">
            {chosen.length === 0 ? (
              <>
                <strong>{t('cleanup.effect.none')}</strong>
                <span>
                  {ready && modelCount > 0
                    ? (reclaimable.bytes > 0
                      ? t('cleanup.effect.couldFree', { count: modelCount, amount: gbLabel(reclaimable.bytes) })
                      : t('cleanup.effect.couldFreeUnknown', { count: modelCount }))
                    : ''}
                </span>
              </>
            ) : (
              <>
                <strong>{t('cleanup.effect.selected', { count: chosen.length })}</strong>
                <span>
                  {afterFree != null
                    ? t('cleanup.effect.frees', { freed: gbLabel(frees.bytes), after: gbLabel(afterFree), now: gbLabel(disk.free) })
                    : t('cleanup.effect.freesNoDisk', { freed: gbLabel(frees.bytes) })}
                  {frees.unknown > 0 && ` ${t('cleanup.effect.unknown', { count: frees.unknown })}`}
                </span>
              </>
            )}
            {removalPending && <span className="ledger-effect__wait">{t('cleanup.effect.pending')}</span>}
          </div>
          {chosen.length > 0 && (
            <Button variant="ghost" size="sm" onClick={() => setSelected(new Set())}>
              {t('cleanup.clear')}
            </Button>
          )}
          {chosen.length === 0 && plan.groups.safe.length > 0 && (
            <Button variant="secondary" size="sm" data-testid="cleanup-pick-safe" onClick={() => setSelected(new Set(plan.groups.safe.map(item => item.id)))}>
              {t('cleanup.pickSafe', { count: plan.groups.safe.length })}
            </Button>
          )}
          <Button variant="danger" icon="trash" data-testid="cleanup-remove" disabled={chosen.length === 0 || removalPending || checking} aria-busy={checking || undefined} onClick={review}>
            {t('cleanup.remove', { count: chosen.length })}
          </Button>
        </>
      )}
    >
      {disk && (
        <div className="ledger-space">
          <div
            className="dk-meter"
            role="img"
            aria-label={t('cleanup.meterAria', { other: gbLabel(disk.other), models: gbLabel(disk.models), free: gbLabel(disk.free) })}
          >
            <span className="dk-meter-seg ledger-space__other" style={wStyle(pct(disk.other))} />
            <span className="dk-meter-seg" style={wStyle(Math.max(0, pct(disk.models) - freedFraction))} />
            {freedFraction > 0 && <span className="dk-meter-seg ledger-space__freed" style={wStyle(freedFraction)} />}
          </div>
          <ul className="dk-meter-legend">
            <li><span className="dk-swatch ledger-space__other-swatch" />{t('cleanup.legend.other')} <span className="ledger-space__fig">{gbLabel(disk.other)}</span></li>
            <li><span className="dk-swatch" />{t('cleanup.legend.models')} <span className="ledger-space__fig">{gbLabel(disk.models)}</span></li>
            {freedFraction > 0 && <li><span className="dk-swatch ledger-space__freed-swatch" />{t('cleanup.legend.frees')} <span className="ledger-space__fig">{gbLabel(frees.bytes)}</span></li>}
            <li><span className="dk-swatch ledger-space__free-swatch" />{t('cleanup.legend.free')} <span className="ledger-space__fig">{gbLabel(disk.free)}</span></li>
          </ul>
        </div>
      )}

      <p className="ledger-note" data-testid="cleanup-honesty">
        <Icon name="info" />
        <span>
          <strong>{t('cleanup.noUsage.title')}</strong> {t('cleanup.noUsage.text')}
        </span>
      </p>
      {ready && modelCount + plan.protected.length > 0 && (
        <p className="ledger-note" data-testid="cleanup-sizes">
          <Icon name="hard-drive" />
          <span>{storage.status === 'ready' ? t('cleanup.sizes.disk') : t('cleanup.sizes.estimate')}</span>
        </p>
      )}
      {ready && brokenModels.length > 0 && (
        <p className="ledger-note ledger-note--warn" role="status" data-testid="cleanup-missing">
          <Icon name="alert-circle" /> <span>{t('cleanup.missing.note', { models: brokenModels.join(', ') })}</span>
        </p>
      )}
      {!facts.verified && facts.loaded && (
        <p className="ledger-note ledger-note--warn" role="status" data-testid="cleanup-unverified">
          <Icon name="alert-circle" /> <span>{t('cleanup.unverified')}</span>
        </p>
      )}
      {modelsError && (
        <p className="ledger-note ledger-note--warn" role="alert">
          <Icon name="alert-circle" /> <span>{t('lifecycle.errors.loadList', { message: modelsError })}</span>
        </p>
      )}

      {!ready && (
        <div className="ledger-sheet__loading" data-testid="cleanup-loading">
          {[0, 1, 2, 3].map(i => <span key={i} className="dk-skeleton dk-skeleton--line" />)}
        </div>
      )}

      {ready && modelCount === 0 && (
        <EmptyState
          variant="first-run"
          title={t('cleanup.empty.title')}
          level={3}
          body={t('cleanup.empty.text')}
        />
      )}

      {ready && TIERS.map(tier => {
        const items = plan.groups[tier]
        if (items.length === 0) return null
        const sum = totalSize(items, storage.index)
        const allOn = items.every(item => selected.has(item.id))
        return (
          <section key={tier} className="ledger-tier" data-testid={`cleanup-tier-${tier}`} aria-labelledby={`cleanup-tier-${tier}-h`}>
            <div className="ledger-tier__head">
              <h3 id={`cleanup-tier-${tier}-h`}>{t(`cleanup.tier.${tier}.title`)}</h3>
              <span className="ledger-tier__count">
                {t('cleanup.count', { count: items.length })}
                {sum.bytes > 0 && <> &middot; {gbLabel(sum.bytes)}</>}
              </span>
              <button type="button" className="dk-link ledger-tier__all" onClick={() => toggleTier(tier)}>
                {allOn ? t('cleanup.clearGroup') : t('cleanup.selectAll')}
              </button>
            </div>
            <p className="ledger-tier__hint">{t(`cleanup.tier.${tier}.hint`)}</p>
            <ul className="ledger-tier__list">
              {items.map(item => (
                <li key={item.id}>
                  <label className="ledger-cr" data-selected={selected.has(item.id) ? 'true' : 'false'}>
                    <input
                      className="dk-check"
                      type="checkbox"
                      checked={selected.has(item.id)}
                      onChange={() => toggle(item.id)}
                      aria-label={t('cleanup.selectModel', { model: item.id })}
                    />
                    <span className="ledger-cr__main">
                      <span className="ledger-cr__name">
                        {item.id}
                        {item.backend && <Tag>{item.backend}</Tag>}
                      </span>
                      <span className="ledger-cr__why">{reasonText(t, item.reason)}</span>
                      <FactLines t={t} item={item} />
                    </span>
                    <span className="ledger-cr__size">{sizeText(t, item)}</span>
                  </label>
                </li>
              ))}
            </ul>
          </section>
        )
      })}

      {ready && plan.protected.length > 0 && (
        <section className="ledger-tier ledger-tier--protected" data-testid="cleanup-protected">
          <button
            type="button"
            className="ledger-protected__toggle"
            aria-expanded={showProtected}
            aria-controls="cleanup-protected-list"
            onClick={() => setShowProtected(v => !v)}
          >
            <Icon name={showProtected ? 'chevron-down' : 'chevron-right'} />
            <h3>{t('cleanup.protected.title')}</h3>
            <span className="ledger-tier__count">
              {t('cleanup.count', { count: plan.protected.length })} &middot; {t('cleanup.protected.never')}
            </span>
          </button>
          {showProtected && (
            <ul className="ledger-tier__list" id="cleanup-protected-list">
              {plan.protected.map(item => (
                <li key={item.id} className="ledger-cr ledger-cr--locked">
                  <Icon name="lock" className="ledger-cr__lock" />
                  <span className="ledger-cr__main">
                    <span className="ledger-cr__name">{item.id}</span>
                    <span className="ledger-cr__why">{protectedText(t, item.reasons)}</span>
                    <FactLines t={t} item={item} />
                  </span>
                  <span className="ledger-cr__size">{item.sizeSource === 'disk' || item.size ? sizeText(t, item) : ''}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
      {dupLoading && <span className="dk-sr-only" role="status">{t('cleanup.checkingBuilds')}</span>}
    </SideSheet>

    <ConfirmDialog
        open={!!confirm}
        title={confirmed ? (nothingLeft ? t('cleanup.confirm.none') : t('cleanup.confirm.title', { count: confirmed.stillOk.length })) : ''}
        message={confirmBody}
        confirmLabel={confirmed ? (nothingLeft ? t('cleanup.confirm.close') : t('cleanup.remove', { count: confirmed.stillOk.length })) : ''}
        danger={!nothingLeft}
        onConfirm={doRemove}
        onCancel={() => setConfirm(null)}
      />
    </>
  )
}

function wStyle(fraction) {
  return { '--dk-w': `${Math.max(0, Math.min(1, fraction)) * 100}%` }
}

