import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Popover from '../Popover'
import { Button, Segmented, SearchField } from '../ui'

// The one filter bar of an Administration list: a search field, filter chips,
// a Filters popover for whatever does not fit as a chip, and a sort at the
// right. Every part is optional; a page names the parts it has.
//
//   search   { value, onChange, label, placeholder }
//   chips    { options, value, onChange, label, mode }   (Segmented: options are
//            { id, label, count }; mode "radio" for a value, default a filter)
//   filters  { count, children }   the controls of the popover; `count` is how
//            many of them are set, drawn on the button
//   sort     { value, onChange, options, label }   options are { id, label }
//   extra    a node drawn after the sort (a toggle that belongs to the list)
export default function AdminFilterBar({ search, chips, filters, sort, extra, className = '' }) {
  const { t } = useTranslation('nav')
  const [open, setOpen] = useState(false)
  const anchor = useRef(null)
  const panelId = useId()
  const sortId = useId()
  return (
    <div className={`admin-filters ${className}`.trim()} data-testid="admin-filters">
      {search && (
        <SearchField
          className="admin-filters__search"
          value={search.value}
          onChange={search.onChange}
          label={search.label}
          placeholder={search.placeholder}
        />
      )}
      {chips && (
        <Segmented
          className="admin-filters__chips"
          options={chips.options}
          value={chips.value}
          onChange={chips.onChange}
          label={chips.label}
          mode={chips.mode}
          labelledBy={chips.labelledBy}
        />
      )}
      {filters && (
        <>
          <span ref={anchor} className="admin-filters__more">
            <Button
              size="sm"
              icon="filter"
              aria-haspopup="dialog"
              aria-expanded={open}
              aria-controls={open ? panelId : undefined}
              onClick={() => setOpen(o => !o)}
              data-testid="admin-filters-button"
            >
              {t('admin.filters.label')}
              {filters.count > 0 && <span className="ui-seg__count" aria-label={t('admin.filters.active', { count: filters.count })}>{filters.count}</span>}
            </Button>
          </span>
          <Popover anchor={anchor} open={open} onClose={() => setOpen(false)} ariaLabel={t('admin.filters.label')}>
            <div className="admin-filters__panel" id={panelId} data-testid="admin-filters-panel">{filters.children}</div>
          </Popover>
        </>
      )}
      {(sort || extra) && (
        <div className="admin-filters__end">
          {sort && (
            <label className="admin-filters__sort" htmlFor={sortId}>
              <span>{sort.label || t('admin.filters.sort')}</span>
              <span className="dk-select-wrap">
                <select id={sortId} className="dk-select" value={sort.value} onChange={e => sort.onChange(e.target.value)}>
                  {sort.options.map(o => <option key={o.id} value={o.id}>{o.label}</option>)}
                </select>
              </span>
            </label>
          )}
          {extra}
        </div>
      )}
    </div>
  )
}
