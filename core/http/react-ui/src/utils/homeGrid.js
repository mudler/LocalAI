// Pure layout rules of the Home grid: one size scale (s, m, l) for every
// widget, a fixed column grid, and a packing that leaves no hole. No React, no
// DOM here; Home, the layout editor and the unit tests use the same functions.

export const WIDGET_SIZES = ['s', 'm', 'l']

// The size a page tile had before home.json could say it: its page.json
// widget block's small, medium or large.
const FROM_PAGE = { small: 's', medium: 'm', large: 'l' }
const TO_PAGE = { s: 'small', m: 'medium', l: 'large' }
// What a widget without a size takes. A number is one unit; a list or the
// chats are two columns wide, so they show a few lines.
const DEFAULT_SIZE = { metric: 's', list: 'm', chats: 'm', page: 'm' }

// The window widths, measured on the grid itself, at which it has 4 and 2
// columns; narrower is one column.
export const WIDE_AT = 800
export const MEDIUM_AT = 440

export const isSize = (s) => WIDGET_SIZES.includes(s)

// widgetSize is the size of a widget: its own `size`, else (page tiles) the
// size of the page's widget block, else the default of its type.
export function widgetSize(w, page) {
  if (isSize(w?.size)) return w.size
  if (w?.type === 'page') return FROM_PAGE[page?.widget?.size] || 'm'
  return DEFAULT_SIZE[w?.type] || 's'
}

// pageSizeOf is the size word a page gets in localai.page.size.
export const pageSizeOf = (size) => TO_PAGE[size] || 'medium'

// columnsFor is the number of columns of a grid that is width pixels wide.
export function columnsFor(width) {
  if (!(width > 0)) return 1
  if (width >= WIDE_AT) return 4
  if (width >= MEDIUM_AT) return 2
  return 1
}

// spanOf is the footprint of a size on a grid of cols columns: width in
// columns (s one, m two, l the full row; never more than the grid has) and
// height in row units (one, two, three).
export function spanOf(size, cols) {
  const rh = { s: 1, m: 2, l: 3 }[size] || 1
  const want = { s: 1, m: 2, l: cols }[size] || 1
  return { cw: Math.max(1, Math.min(want, cols)), rh }
}

const free = (g, r, c, w, h) => {
  for (let y = r; y < r + h; y++) for (let x = c; x < c + w; x++) if (g[y]?.[x] !== undefined && g[y][x] !== -1) return false
  return true
}

// packGrid places sizes (in order) on a grid of cols columns the way CSS
// `grid-auto-flow: dense` does, each at the first place it fits, then closes
// every hole: a tile next to an empty cell grows into it, left and right
// first, so the last row's tiles stretch to the full width. Returns
//   cells  [{col, row, cw, rh}] one per size, 0-based, in the order of sizes
//   rows   the height of the grid in units
//   holes  empty cells that no tile could grow into (0 for every mix of sizes
//          the unit tests try)
export function packGrid(sizes, cols) {
  const n = Math.max(1, cols | 0)
  const cells = []
  const g = []
  const row = (r) => { while (g.length <= r) g.push(new Array(n).fill(-1)); return g[r] }
  const put = (i, c) => {
    for (let y = c.row; y < c.row + c.rh; y++) for (let x = c.col; x < c.col + c.cw; x++) row(y)[x] = i
  }
  sizes.forEach((s, i) => {
    const { cw, rh } = spanOf(s, n)
    for (let r = 0; ; r++) {
      let at = -1
      for (let c = 0; c + cw <= n; c++) if (free(g, r, c, cw, rh)) { at = c; break }
      if (at >= 0) { const cell = { col: at, row: r, cw, rh }; cells.push(cell); put(i, cell); break }
    }
  })
  const rows = g.length
  const empty = (r, c) => r >= 0 && r < rows && c >= 0 && c < n && g[r][c] === -1
  const strip = (r0, c0, w, h) => {
    for (let y = r0; y < r0 + h; y++) for (let x = c0; x < c0 + w; x++) if (!empty(y, x)) return false
    return true
  }
  const claim = (i, r0, c0, w, h) => {
    for (let y = r0; y < r0 + h; y++) for (let x = c0; x < c0 + w; x++) g[y][x] = i
  }
  let changed = true
  while (changed) {
    changed = false
    for (let r = 0; r < rows && !changed; r++) {
      for (let c = 0; c < n && !changed; c++) {
        if (g[r][c] !== -1) continue
        const left = c > 0 ? g[r][c - 1] : -1
        const right = c + 1 < n ? g[r][c + 1] : -1
        const above = r > 0 ? g[r - 1][c] : -1
        const below = r + 1 < rows ? g[r + 1][c] : -1
        const L = left >= 0 ? cells[left] : null
        const R = right >= 0 ? cells[right] : null
        const A = above >= 0 ? cells[above] : null
        const B = below >= 0 ? cells[below] : null
        if (L && L.col + L.cw === c && strip(L.row, c, 1, L.rh)) { claim(left, L.row, c, 1, L.rh); L.cw++; changed = true } else if (R && R.col === c + 1 && strip(R.row, c, 1, R.rh)) { claim(right, R.row, c, 1, R.rh); R.col--; R.cw++; changed = true } else if (A && A.row + A.rh === r && strip(r, A.col, A.cw, 1)) { claim(above, r, A.col, A.cw, 1); A.rh++; changed = true } else if (B && B.row === r + 1 && strip(r, B.col, B.cw, 1)) { claim(below, r, B.col, B.cw, 1); B.row--; B.rh++; changed = true }
      }
    }
  }
  let holes = 0
  for (let r = 0; r < rows; r++) for (let c = 0; c < n; c++) if (g[r][c] === -1) holes++
  return { cells, rows, holes }
}
