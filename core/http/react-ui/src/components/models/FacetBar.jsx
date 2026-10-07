import Icon from '../Icon'

// The capability facets as filter chips with counts. A chip is a button with
// aria-pressed, which the kit draws as a check on the accent wash. A count is
// the number of gallery entries the server reports for that facet under the
// current search and backend; it is left out until the server has answered,
// rather than shown as a zero.
export default function FacetBar({ facets, active, counts, stale, isAvailable, onToggle, t, ariaLabel }) {
  // Until the server has answered once, the only honest chips are the one that
  // resets and the ones in use. The rest appear with their counts.
  const answered = counts && Object.keys(counts).length > 0
  return (
    <div className="ledger-facets" role="group" aria-label={ariaLabel}>
      {facets.map(f => {
        const isAll = f.key === ''
        const pressed = isAll ? active.length === 0 : active.includes(f.key)
        const count = counts?.[f.key]
        // A facet nothing matches is clutter, unless it is the one in use.
        if (!isAll && !pressed && (count === 0 || !answered)) return null
        const available = isAvailable(f.key)
        return (
          <button
            key={f.key || 'all'}
            type="button"
            className="dk-chip ledger-facet"
            data-testid={`facet-${f.key || 'all'}`}
            aria-pressed={pressed}
            disabled={!available}
            title={!available ? t('filters.unavailableForBackend') : undefined}
            onClick={() => onToggle(f.key)}
          >
            {f.icon && <Icon name={f.icon} className="ledger-facet__icon" />}
            <span>{t(f.labelKey)}</span>
            {typeof count === 'number' && <span className="ledger-facet__count" data-stale={stale ? 'true' : undefined}>{count}</span>}
          </button>
        )
      })}
    </div>
  )
}
