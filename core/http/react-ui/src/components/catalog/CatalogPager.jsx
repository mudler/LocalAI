import Icon from '../Icon'

// Previous / next controls with the position between them. Hidden when there is
// one page. Presentational: the caller owns the page number.
//
//   page, totalPages   1-based position
//   onPage             (nextPage) => void
//   labels             { previous, next } accessible names
//   className          extra class so a page keeps its own layout rules
export default function CatalogPager({ page, totalPages, onPage, labels, className = '' }) {
  if (totalPages <= 1) return null
  return (
    <div className={`cat-pager ${className}`.trim()}>
      <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => onPage(Math.max(1, page - 1))} disabled={page <= 1} aria-label={labels.previous}>
        <Icon name="chevron-left" />
      </button>
      <span className="dk-mono">{page} / {totalPages}</span>
      <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => onPage(Math.min(totalPages, page + 1))} disabled={page >= totalPages} aria-label={labels.next}>
        <Icon name="chevron-right" />
      </button>
    </div>
  )
}
