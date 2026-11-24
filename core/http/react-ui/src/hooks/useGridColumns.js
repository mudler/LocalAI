import { useEffect, useState } from 'react'
import { columnsFor } from '../utils/homeGrid'

// useGridColumns is the column count of the Home grid, from the width of el
// (the element that holds Home and its layout editor, so the grid and the
// editor's preview always have the same count). It follows the element's
// width, not the window's, so a side panel next to Home changes it too.
export function useGridColumns(el) {
  const [cols, setCols] = useState(1)
  useEffect(() => {
    if (!el) return undefined
    setCols(columnsFor(el.clientWidth))
    if (typeof ResizeObserver !== 'function') return undefined
    const watch = new ResizeObserver(([e]) => setCols(columnsFor(e.contentRect.width)))
    watch.observe(el)
    return () => watch.disconnect()
  }, [el])
  return cols
}
