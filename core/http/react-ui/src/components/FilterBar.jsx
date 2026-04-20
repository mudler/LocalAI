import Toggle from './Toggle'

// FilterBar is the shared search + chip filter + toggles control strip that
// the Backends gallery pioneered. Pulled into its own component so the System
// page's two tabs stop looking like a different app — matching visual
// grammar + matching keyboard behavior.
//
// Props:
//   search:            controlled value for the search input.
//   onSearchChange:    (value) => void; null disables the search input entirely.
//   searchPlaceholder: placeholder for the search input.
//   filters:           [{ key, label, icon }]; activeFilter is compared by key.
//                      Omit to hide the chip row.
//   activeFilter:      currently-selected filter key (use '' for "all" if
//                      that's the first entry in `filters`).
//   onFilterChange:    (key) => void.
//   toggles:           [{ key, label, icon?, checked, onChange }]; optional
//                      right-side toggle group (e.g. "Show all", "Development").
//   rightSlot:         arbitrary element rendered after the toggles — use for
//                      sort controls or extra buttons.
export default function FilterBar({
  search,
  onSearchChange,
  searchPlaceholder = 'Search...',
  filters,
  activeFilter,
  onFilterChange,
  toggles,
  rightSlot,
}) {
  const hasFilters = Array.isArray(filters) && filters.length > 0
  const hasToggles = Array.isArray(toggles) && toggles.length > 0

  return (
    <div className="filter-bar-group">
      {onSearchChange && (
        <div className="search-bar filter-bar-group__search">
          <i className="fas fa-search search-icon" />
          <input
            className="input"
            placeholder={searchPlaceholder}
            value={search ?? ''}
            onChange={e => onSearchChange(e.target.value)}
            aria-label={searchPlaceholder}
          />
        </div>
      )}

      {(hasFilters || hasToggles || rightSlot) && (
        <div className="filter-bar-group__row">
          {hasFilters && (
            <div className="filter-bar" role="tablist" aria-label="Filter">
              {filters.map(f => (
                <button
                  key={f.key}
                  role="tab"
                  aria-selected={activeFilter === f.key}
                  className={`filter-btn ${activeFilter === f.key ? 'active' : ''}`}
                  onClick={() => onFilterChange(f.key)}
                >
                  {f.icon && <i className={`fas ${f.icon}`} style={{ marginRight: 4 }} />}
                  {f.label}
                  {typeof f.count === 'number' && (
                    <span className="filter-btn__count">{f.count}</span>
                  )}
                </button>
              ))}
            </div>
          )}

          {(hasToggles || rightSlot) && (
            <div className="filter-bar-group__right">
              {hasToggles && toggles.map(t => (
                <label key={t.key} className="filter-bar-group__toggle">
                  <Toggle checked={t.checked} onChange={t.onChange} />
                  {t.icon && <i className={`fas ${t.icon}`} />}
                  {t.label}
                </label>
              ))}
              {rightSlot}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
