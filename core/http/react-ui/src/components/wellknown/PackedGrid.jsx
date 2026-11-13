import { useLayoutEffect, useMemo, useRef } from 'react'
import { packGrid } from '../../utils/homeGrid'
import './wellKnown.css'

// PackedGrid lays out its children on the Home grid: cols columns, a row unit
// of fixed height, and each child as a block of the span of its size. The
// placement is computed once (packGrid) and written on the children, so Home
// and the layout editor's preview, which use this with the same sizes and
// columns, put every block in the same cell.
//
//   sizes     'm' | 's' | 'l' of each child, in the order of the children
//   units     the content height of each child in row units (contentUnits),
//             or undefined for the units of its size (a page tile)
//   cols      the number of columns
//   className added to the grid
//   testId    data-testid of the grid
//
// Every child is one element. The grid carries its placement in data-cols and
// each child in data-col, data-row, data-cw and data-rh (1-based start, spans).
export default function PackedGrid({ sizes, units = [], cols, className = '', testId, children }) {
  const el = useRef(null)
  const key = `${sizes.join('')}|${sizes.map((_, i) => units[i] ?? '').join(',')}`
  const packed = useMemo(() => packGrid(sizes, cols, units), [key, cols]) // eslint-disable-line react-hooks/exhaustive-deps
  // The placement is a computed grid line, not a style a class can name.
  useLayoutEffect(() => {
    const kids = el.current?.children
    if (!kids) return
    packed.cells.forEach((c, i) => {
      const k = kids[i]
      if (!k) return
      k.style.gridColumn = `${c.col + 1} / span ${c.cw}`
      k.style.gridRow = `${c.row + 1} / span ${c.rh}`
      k.dataset.col = String(c.col + 1)
      k.dataset.row = String(c.row + 1)
      k.dataset.cw = String(c.cw)
      k.dataset.rh = String(c.rh)
    })
  })
  return (
    <div ref={el} className={`wk-widgets ${className}`.trim()} data-cols={cols} data-rows={packed.rows} data-holes={packed.holes} data-testid={testId}>
      {children}
    </div>
  )
}
