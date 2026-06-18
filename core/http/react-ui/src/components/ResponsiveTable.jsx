import { useRef, useEffect } from 'react'

// Wraps a standard .table and makes it reflow into stacked label/value cards on
// narrow screens. Column labels are derived from the <thead> and mirrored onto
// each body cell via data-label (read by CSS ::before in the mobile layout), so
// any table becomes responsive without hand-labelling every <td>.
export default function ResponsiveTable({ children, className = '', style, containerStyle }) {
  const ref = useRef(null)

  useEffect(() => {
    const table = ref.current
    if (!table) return
    const apply = () => {
      // Direct children only, so a nested table inside a cell is left alone.
      const heads = [...table.querySelectorAll(':scope > thead > tr > th')].map(th => th.textContent.trim())
      table.querySelectorAll(':scope > tbody > tr').forEach(tr => {
        const cells = [...tr.children]
        // Skip detail/expansion rows (a single cell spanning the table).
        if (cells.length === 1 && cells[0].colSpan > 1) return
        cells.forEach((td, i) => {
          if (heads[i]) td.setAttribute('data-label', heads[i])
        })
      })
    }
    apply()
    // Re-apply when rows change (sort, paging, live data). setAttribute touches
    // attributes only, so a childList/subtree observer won't retrigger itself.
    const obs = new MutationObserver(apply)
    obs.observe(table, { childList: true, subtree: true })
    return () => obs.disconnect()
  }, [])

  return (
    <div className="table-container" style={containerStyle}>
      <table ref={ref} className={`table table--responsive ${className}`.trim()} style={style}>
        {children}
      </table>
    </div>
  )
}
