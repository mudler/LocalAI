import { useId, useRef } from 'react'
import Icon from './Icon.jsx'
import { tabId, tabKeyTarget, tabPanelId, tabStop } from './tabsModel.js'

// Tabs: views of one region, one shown at a time. A tab list (role tablist)
// and its tabs (role tab); the panel is <TabPanel>. To filter a list, use
// Segmented; to set a value, Segmented mode="radio".
//
// The list is one tab stop. The arrow keys of its axis, Home and End move the
// focus and the selection together (automatic activation), skip disabled tabs
// and wrap at both ends.
//
//   items       [{ id, label, icon, count, countTone, countLabel, countHidden,
//                  countTestId, after, disabled, testId, keyshortcuts }]
//               count        a number drawn as a badge after the label; leave
//                            it out (or null) to draw none
//               countTone    accent | warn | error, for a count that needs a look
//               countLabel   what a screen reader says for the count
//               countHidden  hide the count from a screen reader (the label
//                            already says it)
//               after        a node drawn after the count, for a second badge
//   value       the id of the selected tab
//   onChange    called with the id of the tab chosen
//   idPrefix    the tab ids are `<prefix>-tab-<id>`, the panel ids
//               `<prefix>-panel-<id>`. Without it (and without panelId) the
//               tabs get generated ids and no aria-controls: for a caller that
//               draws its panels itself
//   panelId     one panel for every tab: its id
//   label       accessible name of the list; labelledBy: id of an element that
//               names it, used instead
//   variant     underline (default) | pill: the segmented tray, for a view
//               switch that sits in a header or a small card
//   orientation horizontal (default) | vertical
//   listRef     a ref to the tablist element (to scroll or measure it)
//   testId      data-testid of the list; itemTestId: a prefix, each tab gets
//               data-testid prefix + id unless the item has its own testId
//
// Other props (onScroll, data-*) go to the tablist element.
export default function Tabs({
  items, value, onChange, idPrefix, panelId, label, labelledBy,
  variant = 'underline', orientation = 'horizontal', className = '',
  listRef, testId, itemTestId, ...rest
}) {
  const refs = useRef([])
  const autoId = useId()
  const prefix = idPrefix ?? `tabs${autoId.replace(/[^a-zA-Z0-9_-]/g, '')}`
  const wired = idPrefix != null || panelId != null
  const stop = tabStop(items, value)
  const pill = variant === 'pill'

  const onKeyDown = (e, i) => {
    if (e.altKey || e.ctrlKey || e.metaKey) return
    const to = tabKeyTarget(items, i, e.key, orientation)
    if (to < 0) return
    e.preventDefault()
    refs.current[to]?.focus()
    if (items[to].id !== value) onChange?.(items[to].id)
  }

  return (
    <div
      {...rest}
      ref={listRef}
      className={`${pill ? 'dk-segmented' : 'dk-tabs'} ui-tabs ${className}`.trim()}
      role="tablist"
      aria-label={labelledBy ? undefined : label}
      aria-labelledby={labelledBy}
      aria-orientation={orientation === 'vertical' ? 'vertical' : undefined}
      data-variant={variant}
      data-testid={testId}
    >
      {items.map((it, i) => {
        const on = it.id === value
        return (
          <button
            key={it.id}
            ref={el => { refs.current[i] = el }}
            type="button"
            role="tab"
            id={tabId(prefix, it.id)}
            className={pill ? 'dk-seg ui-tabs__tab' : 'dk-tab ui-tabs__tab'}
            aria-selected={on}
            aria-controls={wired ? tabPanelId(prefix, it.id, panelId) : undefined}
            aria-keyshortcuts={it.keyshortcuts}
            tabIndex={i === stop ? 0 : -1}
            disabled={it.disabled}
            data-tab={it.id}
            data-testid={it.testId ?? (itemTestId ? `${itemTestId}${it.id}` : undefined)}
            onClick={() => { if (!on) onChange?.(it.id) }}
            onKeyDown={e => onKeyDown(e, i)}
          >
            {it.icon && <Icon name={it.icon} className="ui-tabs__icon" />}
            {it.label}
            {it.count != null && (
              <span
                className={`dk-badge dk-badge--count ui-tabs__count${it.countTone ? ` dk-badge--${it.countTone}` : ''}`}
                aria-label={it.countHidden ? undefined : it.countLabel}
                aria-hidden={it.countHidden ? 'true' : undefined}
                data-testid={it.countTestId}
              >
                {it.count}
              </span>
            )}
            {it.after}
          </button>
        )
      })}
    </div>
  )
}

// The panel of the selected tab. Give it the same idPrefix (or panelId) as
// the list. `tabIndex` 0 when the panel holds nothing focusable at its start,
// so a keyboard reaches its text. `flush` drops the kit's space above the
// panel, for a panel that sits in a stack with its own gap.
export function TabPanel({ idPrefix, value, panelId, flush = false, className = '', testId, children, ...rest }) {
  return (
    <div
      {...rest}
      role="tabpanel"
      id={tabPanelId(idPrefix, value, panelId)}
      aria-labelledby={tabId(idPrefix, value)}
      className={`${flush ? '' : 'dk-tabpanel '}${className}`.trim() || undefined}
      data-testid={testId}
    >
      {children}
    </div>
  )
}
