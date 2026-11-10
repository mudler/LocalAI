import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Button } from '../ui'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../ActionMenu'
import Icon from '../Icon'

// The parts of the list on the left of the Knowledge and Skills pages. A list
// there can hold hundreds of items, so the toolbar stays one fixed block, the
// rows scroll inside the rail and the rail has a bounded height (see
// library.css). Everything here is presentation and keyboard handling; the
// rules for matching and ordering are in utils/libraryList.js.

// A select for a long list of choices (the source of a skill). Its name is
// for screen readers; the options say the rest.
export function RailSelect({ label, value, onChange, options, testId }) {
  return (
    <span className="dk-select-wrap lib-bar__select">
      <select className="dk-select" aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} data-testid={testId}>
        {options.map(o => <option key={o.id} value={o.id}>{o.label}</option>)}
      </select>
    </span>
  )
}

// The order of the list, as the kebab menu of the toolbar. The open order is
// ticked and named in the button's label.
export function SortMenu({ label, value, onChange, options, testId }) {
  const current = options.find(o => o.id === value)
  const items = options.map(o => ({ key: o.id, label: o.label, shortcut: o.id === value ? '\u2713' : undefined, onClick: () => onChange(o.id) }))
  return (
    <span className="lib-bar__sort" data-testid={testId}>
      <ActionMenu items={items} icon="sort" ariaLabel={label} triggerLabel={`${label}: ${current?.label || ''}`} />
    </span>
  )
}

// A small segmented-looking switch between the two row densities.
export function DensityToggle({ value, onChange, testId }) {
  const { t } = useTranslation('library')
  const compact = value === 'compact'
  return (
    <Button
      variant="ghost"
      icon={compact ? 'list' : 'columns'}
      aria-pressed={compact}
      aria-label={t('rail.compact')}
      title={t('rail.compact')}
      onClick={() => onChange(compact ? 'comfortable' : 'compact')}
      data-testid={testId}
    />
  )
}

// "12 of 200 collections", and a way back when a search or filter hides some.
// The parent words the line (it knows the noun and its plural).
export function CountLine({ text, filtered, onClear, testId }) {
  const { t } = useTranslation('library')
  return (
    <p className="lib-countline" role="status" aria-live="polite" data-testid={testId}>
      <span>{text}</span>
      {filtered && <button type="button" className="dk-link lib-countline__clear" onClick={onClear} data-testid={testId ? `${testId}-clear` : undefined}>{t('rail.clear')}</button>}
    </p>
  )
}

// The small heading above a run of rows (a source, a section).
export function RailHeading({ children, count, id }) {
  return (
    <li className="lib-heading" id={id}>
      <h3 className="lib-heading__text">{children}</h3>
      {count != null && <span className="lib-heading__count">{count}</span>}
    </li>
  )
}

// One row: the button that opens the item, and a quiet menu of its actions
// that shows on hover or keyboard focus (always on a touch screen). The menu is
// a sibling of the button, because a button cannot hold another control.
export function RailRow({ rowKey, testId, selected, stop, onOpen, onFocus, icon, title, mono, meta, metaTestId, end, extra, menu, menuLabel }) {
  return (
    <li className="lib-item" data-selected={selected ? 'true' : undefined}>
      <button
        type="button"
        className="dk-row lib-row"
        aria-current={selected ? 'true' : undefined}
        tabIndex={stop ? 0 : -1}
        data-roving={rowKey}
        data-testid={testId}
        onClick={onOpen}
        onFocus={onFocus}
      >
        <span className="dk-row-lead"><Icon name={icon} /></span>
        <span className="dk-row-main">
          <span className={`dk-row-title lib-row__title${mono ? ' dk-mono' : ''}`} title={title}>{title}</span>
          {meta !== undefined && meta !== '' && <span className="dk-row-meta lib-row__meta" data-testid={metaTestId}>{meta}</span>}
          {extra}
        </span>
        {end && <span className="dk-row-end lib-row__end">{end}</span>}
      </button>
      {menu && menu.length > 0 && (
        <span className="lib-item__menu">
          <ActionMenu items={menu} ariaLabel={menuLabel} compact triggerTabIndex={stop ? 0 : -1} />
        </span>
      )}
    </li>
  )
}

// More rows, a page at a time, below a long list.
export function ShowMore({ remaining, step, onMore, testId }) {
  const { t } = useTranslation('library')
  if (remaining <= 0) return null
  return (
    <li className="lib-more-row" role="presentation">
      <Button variant="ghost" size="sm" onClick={onMore} data-testid={testId}>{t('rail.more', { count: Math.min(step, remaining) })}</Button>
    </li>
  )
}

// What the rail shows while the list loads: the shape of the toolbar and rows,
// so nothing jumps when the data arrives.
export function RailSkeleton({ rows = 7 }) {
  const { t } = useTranslation('library')
  return (
    <div className="dk-split lib-split" aria-busy="true" aria-label={t('rail.loading')} role="status" data-testid="library-loading">
      <div className="dk-split-rail lib-rail">
        <div className="lib-bar">
          <div className="dk-skeleton dk-skeleton--line lib-skel__search" />
          <div className="dk-skeleton dk-skeleton--line lib-skel__bar" />
        </div>
        <div className="lib-scroll">
          {Array.from({ length: rows }, (_, i) => (
            <div key={i} className="lib-skel__row">
              <div className="dk-skeleton lib-skel__lead" />
              <div className="lib-skel__text">
                <div className="dk-skeleton dk-skeleton--line lib-skel__a" />
                <div className="dk-skeleton dk-skeleton--line lib-skel__b" />
              </div>
            </div>
          ))}
        </div>
      </div>
      <section className="dk-card lib-pane">
        <div className="dk-skeleton dk-skeleton--title" />
        <div className="dk-skeleton dk-skeleton--line lib-skel__wide" />
        <div className="dk-skeleton dk-skeleton--block" />
      </section>
    </div>
  )
}

// What the list says when nothing matches: why, and the way out.
export function NoMatch({ text, onClear, testId }) {
  const { t } = useTranslation('library')
  return (
    <div className="lib-nomatch" data-testid={testId}>
      <Icon name="search" />
      <p>{text}</p>
      <Button size="sm" onClick={onClear}>{t('rail.clear')}</Button>
    </div>
  )
}
