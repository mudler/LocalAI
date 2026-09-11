import { useEffect, useRef } from 'react'

// EntityRail is the scannable half of SplitView: one line per entity, grouped
// while browsing and flat while searching.
//
// It is deliberately data-driven rather than aware of models, backends or
// anything else. Each surface maps its own entity onto { id, name, icon, meta }
// and keeps its vocabulary to itself, which is what stops three pages from
// growing three subtly different rails.
//
// Counts describe what is loaded, never a share of some catalog total. Two of
// the three callers page server-side, so a header claiming a total would be
// inventing a number the component cannot know.
//
// items:  [{ id, name, icon, meta, metaTone, stripe, groupId }]
// groups: [{ id, label, icon }] - pass null, or grouped=false, for a flat list.
//   metaTone: 'ok' | 'bad' | 'warn' | 'busy'
//   stripe:   'run' | 'idle' | 'err' | 'off' - a left edge for surfaces read by
//             condition before they are read by name. Omit where state is not
//             the point.
export default function EntityRail({
  items,
  groups = null,
  grouped = false,
  collapsedGroups,
  onToggleGroup,
  selectedId,
  onSelect,
  countLabel,
  actions = null,
  ariaLabel,
  testId = 'entity-rail',
  busy = false,
}) {
  const railRef = useRef(null)
  const lastSelectedIdRef = useRef(null)

  // Narrow layouts hide the rail while detail is selected. When Back (or the
  // browser Back button) clears URL-owned selection, return keyboard focus to
  // the row that opened the detail instead of dropping it on <body>.
  useEffect(() => {
    if (selectedId) {
      lastSelectedIdRef.current = selectedId
      return undefined
    }
    const id = lastSelectedIdRef.current
    if (!id) return undefined
    const frame = window.requestAnimationFrame(() => {
      const item = railRef.current?.querySelector(`[data-entity="${CSS.escape(id)}"]`)
      item?.scrollIntoView({ block: 'nearest' })
      item?.focus({ preventScroll: true })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [selectedId])

  // Up/Down moves the selection so the pane can be stepped through without
  // going back to the mouse.
  //
  // The handler sits on the list AND on every entry. Clicking an entry leaves
  // focus on that <button>, and the key does not reach the container from
  // there, so a container-only handler did nothing on the ordinary path:
  // click a thing, then arrow to the next one.
  const onKeyDown = (e) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    const ids = Array.from(railRef.current?.querySelectorAll('[data-entity]') || [])
      .map(el => el.dataset.entity)
    if (ids.length === 0) return
    e.preventDefault()
    // Whichever of the two handlers has focus must consume the key, or the
    // other acts on it as well and the selection jumps two.
    e.stopPropagation()
    const at = ids.indexOf(selectedId)
    const next = e.key === 'ArrowDown'
      ? (at < 0 ? 0 : Math.min(ids.length - 1, at + 1))
      : (at < 0 ? ids.length - 1 : Math.max(0, at - 1))
    onSelect(ids[next])
    // Move focus with the selection, not just the highlight. Roving tabindex
    // means the newly selected entry is the one tab stop, so leaving focus
    // behind would strand the keyboard on an entry that is no longer reachable
    // by Tab.
    const el = railRef.current?.querySelector(`[data-entity="${CSS.escape(ids[next])}"]`)
    el?.scrollIntoView({ block: 'nearest' })
    el?.focus()
  }

  // Roving tabindex: the rail is one tab stop, and the arrows move inside it.
  // Without this every entry is its own stop, so tabbing past a forty-entry
  // rail to reach the pane is forty keystrokes.
  const firstId = items[0]?.id
  const lastSelectedId = lastSelectedIdRef.current
  const tabbableId = items.some(i => i.id === selectedId)
    ? selectedId
    : items.some(i => i.id === lastSelectedId)
      ? lastSelectedId
      : firstId

  const renderItem = (item) => (
    <RailItem
      key={item.id}
      item={item}
      selected={item.id === selectedId}
      tabbable={item.id === tabbableId}
      onSelect={onSelect}
      onKeyDown={onKeyDown}
      testId={testId}
    />
  )

  const useGroups = grouped && Array.isArray(groups) && groups.length > 0

  return (
    <div className={`entity-rail${busy ? ' entity-rail--busy' : ''}`}>
      <div className="entity-rail__head">
        <span className="entity-rail__count">{countLabel}</span>
        {actions}
      </div>
      {/* A refetch dims and bars the list it is replacing rather than
          unmounting the view. Unmounting took the search box with it, so the
          field you were typing into vanished and focus went to the body. */}
      <div className="entity-rail__progress" aria-hidden={!busy} />

      {/* Not role="listbox". A listbox may only contain options and groups, and
          the collapse control for each group is a button that has to live
          inside the scroller with the entries it folds. Buttons in a labelled
          group is the honest description of what this is, and it costs nothing:
          selection is announced by aria-current and the arrow keys still work. */}
      <div
        className="entity-rail__list"
        role="group"
        aria-label={ariaLabel}
        aria-busy={busy || undefined}
        ref={railRef}
        onKeyDown={onKeyDown}
      >
        {useGroups
          ? groups.map(group => {
            const inGroup = items.filter(i => i.groupId === group.id)
            if (inGroup.length === 0) return null
            const open = !collapsedGroups?.has(group.id)
            return (
              <div className="entity-rail__group" key={group.id}>
                <button
                  type="button"
                  className="entity-rail__group-head"
                  aria-expanded={open}
                  data-testid={`${testId}-group-${group.id}`}
                  onClick={() => onToggleGroup(group.id)}
                >
                  <i className={`fas fa-chevron-${open ? 'down' : 'right'} entity-rail__caret`} aria-hidden="true" />
                  {group.icon && <i className={`fas ${group.icon} entity-rail__group-icon`} aria-hidden="true" />}
                  <span className="entity-rail__group-label">{group.label}</span>
                  <span className="entity-rail__group-count">{inGroup.length}</span>
                </button>
                {open && inGroup.map(renderItem)}
              </div>
            )
          })
          : items.map(renderItem)}
      </div>
    </div>
  )
}

// One line: what it is, and its single most decision-relevant fact. Anything
// that needs a sentence belongs in the pane.
function RailItem({ item, selected, tabbable, onSelect, onKeyDown, testId }) {
  return (
    <button
      type="button"
      aria-current={selected ? 'true' : undefined}
      tabIndex={tabbable ? 0 : -1}
      data-entity={item.id}
      data-testid={`${testId}-item`}
      className={`entity-rail__item${selected ? ' entity-rail__item--on' : ''}`}
      onClick={() => onSelect(item.id)}
      onKeyDown={onKeyDown}
    >
      {item.stripe && <span className={`entity-rail__stripe entity-rail__stripe--${item.stripe}`} />}
      {item.icon && <i className={`fas ${item.icon} entity-rail__icon`} aria-hidden="true" />}
      <span className="entity-rail__main">
        <span className="entity-rail__name">{item.name}</span>
        {item.meta && (
          <span className={`entity-rail__meta${item.metaTone ? ` entity-rail__meta--${item.metaTone}` : ''}`}>
            {item.meta}
          </span>
        )}
      </span>
    </button>
  )
}
