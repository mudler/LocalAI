import { useCallback, useEffect, useRef } from 'react'
import Icon from '../Icon'
import FitCell from './FitCell'
import { rowKeyDown, useRestoreRowFocus } from './rowKeys'
import { fitFor, fitStyle, gbLabel } from '../../utils/modelLedger'
import { publishWalk } from '../../utils/modelWalk'
import { ENTITY_GROUPS, groupForEntity } from '../../utils/entityGroups'

const GROUP_ORDER = ['text', 'vision', 'audio', 'visual', 'other']

function SortHead({ col, label, sort, order, onSort, className }) {
  // A list that cannot be sorted (an online search keeps its provider's order)
  // shows the plain heading.
  if (!onSort) return <th scope="col" className={className}>{label}</th>
  const active = sort === col
  return (
    <th scope="col" className={className} aria-sort={active ? (order === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" className="dk-table-sort" onClick={() => onSort(col)}>
        {label}
        <Icon name="arrow-up" />
      </button>
    </th>
  )
}

// eslint-disable-next-line no-unused-vars
function SkeletonRows({ count = 8 }) {
  return (
    <tbody data-testid="gallery-loader" className="ledger-skeleton">
      {Array.from({ length: count }, (_, i) => (
        <tr key={i} className="dk-table-loading" aria-hidden="true">
          <td className="ledger-mark-cell" />
          <td><span className="dk-skeleton dk-skeleton--line ledger-skeleton__name" /><span className="dk-skeleton dk-skeleton--line ledger-skeleton__sub" /></td>
          <td className="dk-hide-phone"><span className="dk-skeleton dk-skeleton--line" /></td>
          <td className="ledger-fit-cell"><span className="dk-skeleton dk-skeleton--line" /></td>
          <td className="ledger-status"><span className="dk-skeleton dk-skeleton--line ledger-skeleton__action" /></td>
        </tr>
      ))}
    </tbody>
  )
}

// The Explore ledger: one dense table of gallery models, each row carrying its
// own fit on this machine. Selection is a surface step and a check mark.
// Arrow keys move it, Enter installs, Esc closes the inspector.
//
// The rows of an online search use the same table. Such a row sets `title` (the
// repository, shown instead of its key), `sub` (its own line under the name),
// `gated` and `remote`; `heading` puts a section header over a flat list, and
// `onRowVisible` is told, once per row, when it scrolls into view so its size
// can be fetched lazily. A table with no `onSort` has plain column headings.
//
// `sections` adds online sections under the rows of `models`, each with its own
// heading and its own state: `{ id, heading, models, loading, error, onRetry,
// empty, more }`. A section that is loading shows placeholder rows, one that
// failed shows a line with a retry button (and keeps any rows it had), one that
// found nothing says so, and `more` (`{ busy, onMore }`) adds a button under
// its rows. The rows of a section are online rows, so they read `remoteOps`
// (the same functions the page passes for an online table: `estimates`,
// `pending`, `isInstalling`, `progressOf`, `failedOp`, `onInstall`, `onRetry`),
// while `models` keep using the props above.
export default function ExploreTable({
  models, loading, grouped, collapsedGroups, onToggleGroup,
  selectedName, onSelect, onOpen, onInstall, onRetry,
  estimates, pendingEstimates, contextSize, contextLabel, budget, ramAvailable,
  isInstalling, progressOf, failedOp,
  sort, order, onSort, density, heading, onRowVisible, sections: onlineSections = [], remoteOps, t,
}) {
  const bodyRef = useRef(null)
  useRestoreRowFocus(selectedName, bodyRef)
  const visibleRef = useRef(onRowVisible)
  visibleRef.current = onRowVisible

  const entries = models.map(model => ({ model, name: model.name || model.id }))
  // The order a reader expects: what most people install first, then the rest.
  // The shared group list is ordered for matching (specific before general),
  // which is a different job.
  const sections = grouped
    ? [...ENTITY_GROUPS].sort((a, b) => GROUP_ORDER.indexOf(a.id) - GROUP_ORDER.indexOf(b.id))
      .map(g => ({ id: g.id, label: t(g.labelKey), icon: g.icon, items: entries.filter(e => groupForEntity(e.model).id === g.id) }))
      .filter(s => s.items.length > 0)
    : [{ id: null, items: entries }]
  const remoteRows = models.length > 0 && models.every(m => m.remote)
  const libraryNames = sections.flatMap(s => (s.id && collapsedGroups.has(s.id) ? [] : s.items.map(e => e.name)))
  const onlineNames = onlineSections.flatMap(s => s.models.map(m => m.name))
  const names = [...libraryNames, ...onlineNames]

  const move = useCallback((name) => {
    onSelect(name, { replace: true })
    const el = bodyRef.current?.querySelector(`[data-entity="${CSS.escape(name)}"]`)
    el?.focus()
  }, [onSelect])

  // What "previous" and "next" on a model's page walk: the rows on screen, in
  // the order they are shown, without the ones a collapsed group hides.
  // An online result has no page of its own to walk to.
  const walkOrder = remoteRows ? '' : libraryNames.join('\n')
  useEffect(() => { publishWalk('explore', walkOrder ? walkOrder.split('\n') : []) }, [walkOrder])

  // Tell the page which rows have been seen, so an online row's size is
  // fetched when the row is on screen and not for a list nobody scrolled to.
  const rowList = names.join('\n')
  useEffect(() => {
    if (!visibleRef.current || typeof IntersectionObserver === 'undefined' || !bodyRef.current) return undefined
    const seen = new Set()
    const io = new IntersectionObserver(items => {
      for (const item of items) {
        const name = item.target.getAttribute('data-entity')
        if (item.isIntersecting && name && !seen.has(name)) {
          seen.add(name)
          visibleRef.current?.(name)
          io.unobserve(item.target)
        }
      }
    }, { rootMargin: '120px' })
    bodyRef.current.querySelectorAll('tr[data-entity]').forEach(el => io.observe(el))
    return () => io.disconnect()
  }, [rowList])

  // The row that takes Tab: the selected one, else the first.
  const tabbable = names.includes(selectedName) ? selectedName : names[0]

  const renderRow = ({ model, name }) => {
    // An online row in a table of library rows reads the online functions.
    const ops = model.remote && remoteOps ? remoteOps : { estimates, pending: pendingEstimates, isInstalling, progressOf, failedOp, onInstall, onRetry }
    const est = ops.estimates[name]
    const vram = est?.estimates?.[String(contextSize)]?.vramBytes
    const fit = fitFor(vram, budget, ramAvailable)
    const selected = name === selectedName
    const installing = ops.isInstalling(name)
    const progress = ops.progressOf(name)
    const failed = !installing && !model.installed ? ops.failedOp(name) : null
    const sub = model.sub ?? [model.backend, model.license].filter(Boolean).join(' · ')
    const title = model.title || name

    return (
      <tr
        key={name}
        data-row
        data-clickable
        data-entity={name}
        data-testid="discover-rail-item"
        data-selected={selected ? 'true' : 'false'}
        data-error={failed ? '' : undefined}
        aria-current={selected ? 'true' : undefined}
        tabIndex={name === tabbable ? 0 : -1}
        onClick={() => onSelect(name)}
        onDoubleClick={() => onOpen?.(name)}
        onKeyDown={e => rowKeyDown(e, {
          names,
          current: name,
          move,
          enter: n => { if (!model.installed && !ops.isInstalling(n) && !model.installBlocked) ops.onInstall(n) },
          close: () => onSelect(null),
        })}
      >
        <td className="ledger-mark-cell">
          <span className="ledger-mark" aria-hidden="true"><Icon name="check" /></span>
        </td>
        <td className="ledger-name-cell">
          <span className="dk-table-name ledger-name"><span className="ledger-name__text">{title}</span></span>
          <span className="dk-table-sub" title={failed ? failed.error : undefined}>
            {failed ? failed.error : (sub || ' ')}
            {!failed && model.gated && <span className="ledger-builds ledger-builds--gated" data-testid="row-gated">{t('remote.gated')}</span>}
            {!failed && model.has_variants && <span className="ledger-builds">{t('ledger.builds')}</span>}
          </span>
        </td>
        <td className="dk-num dk-hide-phone ledger-size">
          {est?.sizeBytes ? gbLabel(est.sizeBytes) : (ops.pending?.has(name) ? <span className="dk-skeleton dk-skeleton--line" /> : '—')}
        </td>
        <td className="ledger-fit-cell">
          <FitCell fit={fit} pending={ops.pending?.has(name)} t={t} contextLabel={contextLabel} />
        </td>
        <td className="dk-table-actions ledger-status">
          <span className="ledger-rowactions">
            {installing ? (
              <span className="ledger-status__busy" role="status">
                <span className="operation-spinner" aria-hidden="true" />
                {progress > 0 ? t('table.installingPct', { percent: Math.round(progress) }) : t('table.installing')}
                {progress > 0 && (
                  <span className="ledger-progress" aria-hidden="true"><span className="ledger-progress__bar" style={fitStyle(progress / 100)} /></span>
                )}
              </span>
            ) : model.installed ? (
              <span className="ledger-status__done"><Icon name="check" /> {t('table.installed')}</span>
            ) : failed ? (
              <button
                type="button"
                className="dk-btn dk-btn--secondary dk-btn--sm"
                data-testid="discover-row-retry"
                aria-label={t('ledger.retryNamed', { model: title })}
                onClick={e => { e.stopPropagation(); ops.onRetry(name, failed) }}
              >
                <Icon name="refresh" /> {t('ledger.retry')}
              </button>
            ) : (
              <button
                type="button"
                className="dk-btn dk-btn--primary dk-btn--sm"
                data-testid="discover-row-install"
                aria-label={t('ledger.installNamed', { model: title })}
                disabled={!!model.installBlocked}
                title={model.installBlocked ? t('remote.gatedHfInstall') : undefined}
                onClick={e => { e.stopPropagation(); ops.onInstall(name) }}
              >
                <Icon name="download" /> {t('actions.install')}
              </button>
            )}
            {onOpen && !model.remote && (
              <button
                type="button"
                className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm ledger-open"
                data-row-open
                data-testid="row-open"
                aria-label={t('page.openFor', { model: title })}
                title={t('page.openFor', { model: title })}
                onClick={e => { e.stopPropagation(); onOpen(name) }}
              >
                <Icon name="arrow-right" />
              </button>
            )}
          </span>
        </td>
      </tr>
    )
  }

  const renderOnline = (sec) => [
    <tr key={`o-${sec.id}`} className="ledger-group ledger-group--static" data-testid="discover-section-heading" data-section={sec.id}>
      <td colSpan={5}>
        <div className="ledger-group__toggle ledger-group__toggle--static">
          {sec.heading.icon && <Icon name={sec.heading.icon} className="ledger-group__icon" />}
          <span className="ledger-group__label">{sec.heading.label}</span>
          {sec.heading.tag && <span className="ledger-group__tag">{sec.heading.tag}</span>}
          <span className="ledger-group__count">{sec.models.length}</span>
        </div>
      </td>
    </tr>,
    ...sec.models.map(m => renderRow({ model: m, name: m.name })),
    ...(sec.loading && sec.models.length === 0
      ? Array.from({ length: 3 }, (_, i) => (
        <tr key={`o-${sec.id}-sk-${i}`} className="dk-table-loading" data-testid="discover-section-loading" data-section={sec.id} aria-hidden="true">
          <td className="ledger-mark-cell" />
          <td><span className="dk-skeleton dk-skeleton--line ledger-skeleton__name" /><span className="dk-skeleton dk-skeleton--line ledger-skeleton__sub" /></td>
          <td className="dk-hide-phone"><span className="dk-skeleton dk-skeleton--line" /></td>
          <td className="ledger-fit-cell"><span className="dk-skeleton dk-skeleton--line" /></td>
          <td className="ledger-status"><span className="dk-skeleton dk-skeleton--line ledger-skeleton__action" /></td>
        </tr>
      ))
      : []),
    ...(sec.error
      ? [(
        <tr key={`o-${sec.id}-err`} className="ledger-section-note" data-testid="discover-section-error" data-section={sec.id}>
          <td colSpan={5}>
            <div className="ledger-section-note__row" role="alert">
              <span className="ledger-section-note__text">{sec.error.text}</span>
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={sec.onRetry}>
                <Icon name="refresh" /> {t('ledger.retry')}
              </button>
            </div>
          </td>
        </tr>
      )]
      : []),
    ...(sec.empty && !sec.loading && !sec.error && sec.models.length === 0
      ? [(
        <tr key={`o-${sec.id}-empty`} className="ledger-section-note" data-testid="discover-section-empty" data-section={sec.id}>
          <td colSpan={5}><div className="ledger-section-note__row"><span className="ledger-section-note__text">{sec.empty}</span></div></td>
        </tr>
      )]
      : []),
    ...(sec.more
      ? [(
        <tr key={`o-${sec.id}-more`} className="ledger-section-note" data-section={sec.id}>
          <td colSpan={5}>
            <div className="ledger-section-note__row ledger-section-note__row--center">
              <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" data-testid="discover-section-more" disabled={sec.more.busy} onClick={sec.more.onMore}>
                {sec.more.busy ? <Icon name="spinner" spin /> : <Icon name="plus" />} {t('remote.loadMore')}
              </button>
            </div>
          </td>
        </tr>
      )]
      : []),
  ]

  return (
    <div
      className="dk-table-wrap ledger-wrap"
      role="region"
      aria-label={t('title')}
      data-testid="discover-rail"
    >
      <table className={`dk-table ledger-table${density === 'compact' ? ' dk-table--compact' : ''}`} aria-busy={loading || undefined}>
        <caption className="dk-sr-only">{t('ledger.caption', { context: contextLabel })}</caption>
        <thead>
          <tr>
            <th scope="col" className="ledger-mark-cell"><span className="dk-sr-only">{t('ledger.selected')}</span></th>
            <SortHead col="name" label={t('table.modelName')} sort={sort} order={order} onSort={onSort} className="ledger-name-head" />
            <th scope="col" className="dk-num dk-hide-phone">{t('ledger.columns.size')}</th>
            <th scope="col" className="ledger-fit-head">{t('ledger.columns.fit', { context: contextLabel })}</th>
            <SortHead col="status" label={t('table.status')} sort={sort} order={order} onSort={onSort} className="dk-num ledger-status-head" />
          </tr>
        </thead>
        {loading && models.length === 0 ? (
          <>
            <SkeletonRows />
            {onlineSections.length > 0 && <tbody ref={bodyRef}>{onlineSections.map(renderOnline)}</tbody>}
          </>
        ) : (
          <tbody ref={bodyRef}>
            {heading && (
              <tr className="ledger-group ledger-group--static" data-testid="discover-rail-heading">
                <td colSpan={5}>
                  <div className="ledger-group__toggle ledger-group__toggle--static">
                    {heading.icon && <Icon name={heading.icon} className="ledger-group__icon" />}
                    <span className="ledger-group__label">{heading.label}</span>
                    {heading.tag && <span className="ledger-group__tag">{heading.tag}</span>}
                    <span className="ledger-group__count">{models.length}</span>
                  </div>
                </td>
              </tr>
            )}
            {sections.map(section => (
              section.id ? [
                <tr key={`g-${section.id}`} className="ledger-group">
                  <td colSpan={5}>
                    <button
                      type="button"
                      className="ledger-group__toggle"
                      data-testid={`discover-rail-group-${section.id}`}
                      aria-expanded={!collapsedGroups.has(section.id)}
                      onClick={() => onToggleGroup(section.id)}
                    >
                      <Icon name={collapsedGroups.has(section.id) ? 'chevron-right' : 'chevron-down'} className="ledger-group__caret" />
                      <Icon name={section.icon} className="ledger-group__icon" />
                      <span className="ledger-group__label">{section.label}</span>
                      <span className="ledger-group__count">{section.items.length}</span>
                    </button>
                  </td>
                </tr>,
                ...(collapsedGroups.has(section.id) ? [] : section.items.map(renderRow)),
              ] : section.items.map(renderRow)
            ))}
            {onlineSections.map(renderOnline)}
          </tbody>
        )}
      </table>
    </div>
  )
}
