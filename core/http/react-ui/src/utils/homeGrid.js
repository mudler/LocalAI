// Pure layout rules of the Home grid: one size scale (s, m, l) for every
// widget, a fixed column grid, and a dense packing whose last row is full. A
// built-in widget is as high as its content, in whole row units, never more
// than the units of its size. No React, no DOM here; Home, the layout editor
// and the unit tests use the same functions.

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

// The row unit and the gap of the grid (--wk-unit and --wk-gap of .wk-widgets
// in wellKnown.css): n units are n * UNIT_PX + (n - 1) * GAP_PX high.
export const UNIT_PX = 132
export const GAP_PX = 16

// unitsForPx is the number of row units a block of px pixels needs.
export const unitsForPx = (px) => Math.max(1, Math.ceil(((px > 0 ? px : 0) + GAP_PX) / (UNIT_PX + GAP_PX)))

// The parts of a built-in widget, in pixels, as wellKnown.css draws them,
// rounded up so the content always fits: the padding and the title (head),
// one row of a list or of the chats, the line of an empty widget, a number and
// its note on one line (a metric one unit high), a number, its note and the
// sparkline under it, and a footer of one line.
const PX = { head: 60, row: 37, empty: 22, value: 32, spark: 128, footer: 30 }
// The rows a widget shows (HomeWidgets): a list its first eight items, the
// chats the last five.
export const LIST_ROWS = 8
export const CHATS_ROWS = 5

// contentUnits is the height in row units of the content of a built-in widget,
// from what it shows, not from the DOM, so the packing is known before the
// widget draws:
//
//   kind    'list', 'chats' or 'metric'
//   rows    the items of a list or the chats (0 shows the line of an empty widget)
//   spark   a metric with a sparkline
//   footer  the widget shows its footer (a trigger, a stale or broken file)
//
// The grid takes the smaller of this and the units of the widget's size.
export function contentUnits(kind, { rows = 0, spark = false, footer = false } = {}) {
  const foot = footer ? PX.footer : 0
  if (kind === 'metric') return unitsForPx(PX.head + (spark ? PX.spark : PX.value) + foot)
  const max = kind === 'list' ? LIST_ROWS : kind === 'chats' ? CHATS_ROWS : 0
  if (!max) return 1
  const n = Math.min(Math.max(0, rows | 0), max)
  return unitsForPx(PX.head + (n ? n * PX.row : PX.empty) + foot)
}

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

// unitsKey names a widget of home.json for the units its content reported:
// Home and the layout editor's preview read the same ones.
export const unitsKey = (w) => `${w?.id || ''}|${w?.type || ''}|${w?.source || ''}`

// widgetUnits is the content height of a widget in row units: the chats from
// the number of chats, a list or a metric from what it reported (reported, by
// unitsKey), and undefined (the units of its size) for a page tile or a widget
// that has not drawn yet.
export function widgetUnits(w, reported, chats) {
  if (w?.type === 'chats') return contentUnits('chats', { rows: chats })
  if (w?.type === 'page') return undefined
  return reported?.[unitsKey(w)]
}

// packGrid places sizes (in order) on a grid of cols columns the way CSS
// `grid-auto-flow: dense` does, each at the first place it fits. units, when
// given, is the height of the content of each widget in row units
// (contentUnits): a widget is the smaller of that and the units of its size;
// a widget with no units (a page tile) takes the units of its size. A short
// widget that leaves room under it that no later widget took grows back into
// it, never past the units of its size. Dense flow
// already fills a hole that a later, smaller tile fits in; a hole it leaves
// higher up stays. A hole in the last row closes: a tile that ends in that
// row grows into it sideways (from the left first, then the right), or the
// tile right above it grows down, so the last row is full. Returns
//   cells  [{col, row, cw, rh}] one per size, 0-based, in the order of sizes
//   rows   the height of the grid in units
//   holes  the empty cells left (none in the last row)
export function packGrid(sizes, cols, units = []) {
  const n = Math.max(1, cols | 0)
  const cells = []
  const caps = []
  const g = []
  const row = (r) => { while (g.length <= r) g.push(new Array(n).fill(-1)); return g[r] }
  const put = (i, c) => {
    for (let y = c.row; y < c.row + c.rh; y++) for (let x = c.col; x < c.col + c.cw; x++) row(y)[x] = i
  }
  sizes.forEach((s, i) => {
    const { cw, rh: cap } = spanOf(s, n)
    const u = units[i]
    const rh = Number.isInteger(u) && u >= 1 ? Math.min(cap, u) : cap
    caps.push(cap)
    for (let r = 0; ; r++) {
      let at = -1
      for (let c = 0; c + cw <= n; c++) if (free(g, r, c, cw, rh)) { at = c; break }
      if (at >= 0) { const cell = { col: at, row: r, cw, rh }; cells.push(cell); put(i, cell); break }
    }
  })
  const rows = g.length
  const last = rows - 1
  const empty = (r, c) => r >= 0 && r < rows && c >= 0 && c < n && g[r][c] === -1
  const strip = (r0, c0, w, h) => {
    for (let y = r0; y < r0 + h; y++) for (let x = c0; x < c0 + w; x++) if (!empty(y, x)) return false
    return true
  }
  const claim = (i, r0, c0, w, h) => {
    for (let y = r0; y < r0 + h; y++) for (let x = c0; x < c0 + w; x++) g[y][x] = i
  }
  // A short widget grows down into the empty room right under it, up to
  // the units of its size.
  let grew = true
  while (grew) {
    grew = false
    cells.forEach((t, i) => {
      const below = t.row + t.rh
      if (t.rh < caps[i] && below < rows && strip(below, t.col, t.cw, 1)) { claim(i, below, t.col, t.cw, 1); t.rh++; grew = true }
    })
  }
  const endsLast = (t) => t && t.row + t.rh === rows
  let changed = rows > 0
  while (changed) {
    changed = false
    for (let c = 0; c < n && !changed; c++) {
      if (g[last][c] !== -1) continue
      const left = c > 0 ? g[last][c - 1] : -1
      const right = c + 1 < n ? g[last][c + 1] : -1
      const above = last > 0 ? g[last - 1][c] : -1
      const L = left >= 0 ? cells[left] : null
      const R = right >= 0 ? cells[right] : null
      const A = above >= 0 ? cells[above] : null
      if (endsLast(L) && L.col + L.cw === c && strip(L.row, c, 1, L.rh)) { claim(left, L.row, c, 1, L.rh); L.cw++; changed = true } else if (endsLast(R) && R.col === c + 1 && strip(R.row, c, 1, R.rh)) { claim(right, R.row, c, 1, R.rh); R.col--; R.cw++; changed = true } else if (A && A.row + A.rh === last && strip(last, A.col, A.cw, 1)) { claim(above, last, A.col, A.cw, 1); A.rh++; changed = true }
    }
  }
  let holes = 0
  for (let r = 0; r < rows; r++) for (let c = 0; c < n; c++) if (g[r][c] === -1) holes++
  return { cells, rows, holes }
}
