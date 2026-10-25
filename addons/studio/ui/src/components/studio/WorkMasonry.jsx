import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../ConfirmDialog'
// eslint-disable-next-line no-unused-vars
import WorkThumb from './WorkThumb'
// eslint-disable-next-line no-unused-vars
import StarButton, { StarGlyph } from './StarButton'
import { relativeTime } from '../../utils/format'
import {
  TYPE_ICON, TYPE_ORDER, columnCount, countByType, dealColumns, filterWork, groupWork,
} from '../../utils/studioWork'

// "Your work": every result this browser has a record of, newest first, as a
// masonry. Related results (one made from another) stack into a project tile.
//
// Filters count results, not tiles, so "Images 10" means ten pictures even when
// four of them sit in one stack. Grouping only applies under All: a type or
// Favourites filter lists the results themselves, which is what the count says.
export default function WorkMasonry({
  items, ready, types, filter, onFilter, group, onGroup, onOpen, onToggleFavourite, onClear,
}) {
  const { t } = useTranslation('media')
  const [confirming, setConfirming] = useState(false)
  const [columns, setColumns] = useState(3)
  const host = useRef(null)

  const counts = useMemo(() => countByType(items), [items])
  const visible = useMemo(() => filterWork(items, filter), [items, filter])
  const tiles = useMemo(() => {
    if (filter === 'all' && group) return groupWork(visible)
    return visible.map(item => ({ kind: 'single', id: item.id, item, latest: item.createdAt }))
  }, [visible, filter, group])

  // Column count follows the width of the page, not the window, so the sidebar
  // being open or shut changes it too.
  useEffect(() => {
    const el = host.current
    if (!el || typeof ResizeObserver === 'undefined') return undefined
    const update = () => setColumns(columnCount(el.clientWidth))
    update()
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const cols = useMemo(() => dealColumns(tiles, columns), [tiles, columns])
  const tabs = ['all', 'favourites', ...TYPE_ORDER.filter(k => types.includes(k))]
  const label = (key) => key === 'all' ? t('studio.work.all')
    : key === 'favourites' ? t('studio.work.favourites')
      : t(`studio.tabs.${key}`)

  return (
    <section className="studio-work" aria-labelledby="studio-work-title" data-testid="studio-work">
      <div className="studio-work__head">
        <h2 id="studio-work-title">{t('studio.work.title')}</h2>
        <span className="studio-work__count" data-testid="studio-work-count">{t('studio.work.results', { count: items.length })}</span>
        <span className="studio-work__tools">
          {filter === 'all' && (
            <label className="studio-work__group">
              <button
                type="button"
                role="switch"
                className="dk-switch"
                aria-checked={group}
                aria-label={t('studio.work.group')}
                onClick={() => onGroup(!group)}
                data-testid="studio-group-switch"
              />
              <span>{t('studio.work.group')}</span>
            </label>
          )}
          {items.length > 0 && (
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setConfirming(true)} data-testid="studio-clear-history">
              <Icon name="trash" /> {t('studio.work.clear')}
            </button>
          )}
        </span>
      </div>

      <div className="studio-filters" role="tablist" aria-label={t('studio.work.filters')}>
        {tabs.map(key => (
          <button
            key={key}
            type="button"
            role="tab"
            className="studio-filter"
            aria-selected={filter === key}
            data-filter={key}
            onClick={() => onFilter(key)}
          >
            {key === 'favourites' && <StarGlyph className="studio-filter__icon" />}
            {label(key)}
            <span className="studio-filter__n">{counts[key]}</span>
          </button>
        ))}
      </div>

      <div ref={host} className="studio-masonry-host">
        {!ready ? (
          <div className="studio-masonry" aria-busy="true" data-testid="studio-work-loading">
            {Array.from({ length: columns }, (_, c) => (
              <div className="studio-col" key={c}>
                <div className="dk-skeleton studio-skel studio-skel--tall" />
                <div className="dk-skeleton studio-skel" />
              </div>
            ))}
          </div>
        ) : tiles.length === 0 ? (
          <div className="studio-empty" data-testid="studio-work-empty">
            <b>{items.length === 0 ? t('studio.work.emptyTitle') : t('studio.work.emptyFilterTitle', { filter: label(filter) })}</b>
            <span>{items.length === 0 ? t('studio.work.emptyBody') : t('studio.work.emptyFilterBody')}</span>
          </div>
        ) : (
          <div className="studio-masonry" data-columns={columns}>
            {cols.map((column, c) => (
              <div className="studio-col" key={c}>
                {column.map(tile => tile.kind === 'project'
                  ? <ProjectTile key={tile.id} tile={tile} t={t} onOpen={onOpen} />
                  : <SingleTile key={tile.id} item={tile.item} t={t} onOpen={onOpen} onToggleFavourite={onToggleFavourite} />)}
              </div>
            ))}
          </div>
        )}
      </div>

      <ConfirmDialog
        open={confirming}
        danger
        title={t('studio.work.clearTitle')}
        message={t('studio.work.clearBody')}
        confirmLabel={t('studio.work.clearConfirm')}
        onConfirm={() => { setConfirming(false); onClear() }}
        onCancel={() => setConfirming(false)}
      />
    </section>
  )
}

// eslint-disable-next-line no-unused-vars
function SingleTile({ item, t, onOpen, onToggleFavourite }) {
  return (
    <div className="studio-tile" data-testid="work-tile" data-type={item.type} data-id={item.id}>
      <button type="button" className="studio-tile__open" onClick={() => onOpen(item.id)} aria-label={t('studio.work.open', { title: item.title || t(`studio.tabs.${item.type}`) })}>
        <span className="studio-tile__media">
          <WorkThumb item={item} />
          <span className="studio-badge"><Icon name={TYPE_ICON[item.type]} />{t(`studio.tabs.${item.type}`)}</span>
        </span>
        <span className="studio-tile__cap">
          <span className="studio-tile__title">{item.title || t(`studio.work.untitled.${item.type}`)}</span>
          <small>{[item.model, relativeTime(item.createdAt)].filter(Boolean).join(' · ')}</small>
        </span>
      </button>
      <StarButton on={item.favourite} onToggle={() => onToggleFavourite(item.id)} className="studio-tile__star" />
    </div>
  )
}

// eslint-disable-next-line no-unused-vars
function ProjectTile({ tile, t, onOpen }) {
  const chain = tile.items
  const shown = chain.slice(0, 4)
  const latest = chain[chain.length - 1]
  return (
    <div className="studio-stack" data-testid="work-project" data-id={tile.id} data-count={chain.length}>
      <button type="button" className="studio-stack__open" onClick={() => onOpen(latest.id)} aria-label={t('studio.work.openProject', { title: tile.title || t('studio.work.project'), count: chain.length })}>
        <span className="studio-stack__in">
          <span className="studio-tile__media">
            <WorkThumb item={tile.cover} />
            <span className="studio-badge"><Icon name="layers" />{t('studio.work.nResults', { count: chain.length })}</span>
          </span>
          <span className="studio-tile__cap">
            <span className="studio-tile__title studio-tile__title--project">{tile.title || t('studio.work.project')}</span>
            <span className="studio-strip" aria-hidden="true">
              {shown.map((m, i) => (
                <span className="studio-strip__step" key={m.id}>
                  {i > 0 && <i className="studio-strip__dash" />}
                  <span className="studio-strip__glyph"><Icon name={TYPE_ICON[m.type]} /></span>
                </span>
              ))}
              {chain.length > shown.length && <span className="studio-strip__more">+{chain.length - shown.length}</span>}
              <i className="studio-strip__dash studio-strip__dash--ghost" />
              <span className="studio-strip__glyph studio-strip__glyph--ghost"><Icon name="plus" /></span>
            </span>
            <small>{relativeTime(tile.latest)}</small>
          </span>
        </span>
      </button>
    </div>
  )
}
