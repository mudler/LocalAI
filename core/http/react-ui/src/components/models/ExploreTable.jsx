import { useCallback, useEffect, useRef } from 'react'
import Icon from '../Icon'
import FitCell from './FitCell'
import { rowKeyDown, useRestoreRowFocus } from './rowKeys'
import { fitFor, fitStyle, gbLabel } from '../../utils/modelLedger'
import { publishWalk } from '../../utils/modelWalk'
import { ENTITY_GROUPS, groupForEntity } from '../../utils/entityGroups'

const GROUP_ORDER = ['text', 'vision', 'audio', 'visual', 'other']

function SortHead({ col, label, sort, order, onSort, className }) {
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
export default function ExploreTable({
  models, loading, grouped, collapsedGroups, onToggleGroup,
  selectedName, onSelect, onOpen, onInstall, onRetry,
  estimates, pendingEstimates, contextSize, contextLabel, budget, ramAvailable,
  isInstalling, progressOf, failedOp,
  sort, order, onSort, density, t,
}) {
  const bodyRef = useRef(null)
  useRestoreRowFocus(selectedName, bodyRef)

  const entries = models.map(model => ({ model, name: model.name || model.id }))
  // The order a reader expects: what most people install first, then the rest.
  // The shared group list is ordered for matching (specific before general),
  // which is a different job.
  const sections = grouped
    ? [...ENTITY_GROUPS].sort((a, b) => GROUP_ORDER.indexOf(a.id) - GROUP_ORDER.indexOf(b.id))
      .map(g => ({ id: g.id, label: t(g.labelKey), icon: g.icon, items: entries.filter(e => groupForEntity(e.model).id === g.id) }))
      .filter(s => s.items.length > 0)
    : [{ id: null, items: entries }]
  const names = sections.flatMap(s => (s.id && collapsedGroups.has(s.id) ? [] : s.items.map(e => e.name)))

  const move = useCallback((name) => {
    onSelect(name, { replace: true })
    const el = bodyRef.current?.querySelector(`[data-entity="${CSS.escape(name)}"]`)
    el?.focus()
  }, [onSelect])

  // What "previous" and "next" on a model's page walk: the rows on screen, in
  // the order they are shown, without the ones a collapsed group hides.
  const walkOrder = names.join('\n')
  useEffect(() => { publishWalk('explore', walkOrder ? walkOrder.split('\n') : []) }, [walkOrder])

  // The row that takes Tab: the selected one, else the first.
  const tabbable = names.includes(selectedName) ? selectedName : names[0]

  const renderRow = ({ model, name }) => {
    const est = estimates[name]
    const vram = est?.estimates?.[String(contextSize)]?.vramBytes
    const fit = fitFor(vram, budget, ramAvailable)
    const selected = name === selectedName
    const installing = isInstalling(name)
    const progress = progressOf(name)
    const failed = !installing && !model.installed ? failedOp(name) : null
    const sub = [model.backend, model.license].filter(Boolean).join(' · ')

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
          enter: n => { if (!model.installed && !isInstalling(n)) onInstall(n) },
          close: () => onSelect(null),
        })}
      >
        <td className="ledger-mark-cell">
          <span className="ledger-mark" aria-hidden="true"><Icon name="check" /></span>
        </td>
        <td className="ledger-name-cell">
          <span className="dk-table-name ledger-name"><span className="ledger-name__text">{name}</span></span>
          <span className="dk-table-sub" title={failed ? failed.error : undefined}>
            {failed ? failed.error : (sub || ' ')}
            {!failed && model.has_variants && <span className="ledger-builds">{t('ledger.builds')}</span>}
          </span>
        </td>
        <td className="dk-num dk-hide-phone ledger-size">
          {est?.sizeBytes ? gbLabel(est.sizeBytes) : (pendingEstimates?.has(name) ? <span className="dk-skeleton dk-skeleton--line" /> : '—')}
        </td>
        <td className="ledger-fit-cell">
          <FitCell fit={fit} pending={pendingEstimates?.has(name)} t={t} contextLabel={contextLabel} />
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
                aria-label={t('ledger.retryNamed', { model: name })}
                onClick={e => { e.stopPropagation(); onRetry(name, failed) }}
              >
                <Icon name="refresh" /> {t('ledger.retry')}
              </button>
            ) : (
              <button
                type="button"
                className="dk-btn dk-btn--primary dk-btn--sm"
                data-testid="discover-row-install"
                aria-label={t('ledger.installNamed', { model: name })}
                onClick={e => { e.stopPropagation(); onInstall(name) }}
              >
                <Icon name="download" /> {t('actions.install')}
              </button>
            )}
            <button
              type="button"
              className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm ledger-open"
              data-row-open
              data-testid="row-open"
              aria-label={t('page.openFor', { model: name })}
              title={t('page.openFor', { model: name })}
              onClick={e => { e.stopPropagation(); onOpen?.(name) }}
            >
              <Icon name="arrow-right" />
            </button>
          </span>
        </td>
      </tr>
    )
  }

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
          <SkeletonRows />
        ) : (
          <tbody ref={bodyRef}>
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
          </tbody>
        )}
      </table>
    </div>
  )
}
