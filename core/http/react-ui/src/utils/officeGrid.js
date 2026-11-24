// A workbook as the grid of text the table view draws: cell addresses, the
// text a cell shows, and the caps on rows and columns. No DOM and no library
// here, so it runs in node; officeXlsx.js reads the file and calls it.
import { SHEET_MAX_COLS, SHEET_MAX_ROWS } from './officePreview.js'

// "A1" -> { r: 0, c: 0 }; the column letters are base 26.
export function parseCellRef(ref) {
  const m = /^\$?([A-Za-z]{1,3})\$?(\d+)$/.exec(ref || '')
  if (!m) return null
  let c = 0
  for (const ch of m[1].toUpperCase()) c = c * 26 + (ch.charCodeAt(0) - 64)
  return { r: Number(m[2]) - 1, c: c - 1 }
}

export function cellRef(r, c) {
  let s = ''
  for (let n = c + 1; n > 0; n = Math.floor((n - 1) / 26)) s = String.fromCharCode(65 + ((n - 1) % 26)) + s
  return `${s}${r + 1}`
}

// "A1:F40" -> { s: {r,c}, e: {r,c} }, or null.
export function parseRange(ref) {
  if (!ref) return null
  const [a, b = a] = String(ref).split(':')
  const s = parseCellRef(a)
  const e = parseCellRef(b)
  return s && e ? { s, e } : null
}

// The text a cell shows: the text the spreadsheet formats it with (dates,
// currency, percent), else its value. A formula that was never calculated
// (a file written by a script has no stored results) shows as the formula.
// Returns { text, num } where num says the cell holds a number or a date.
export function cellText(cell) {
  if (!cell) return { text: '', num: false }
  // A stub: no stored value. A formula there was never calculated.
  if (cell.t === 'z') return { text: cell.f ? `=${cell.f}` : '', num: false }
  const num = cell.t === 'n' || cell.t === 'd'
  if (cell.w !== undefined && cell.w !== null && cell.w !== '') return { text: String(cell.w), num }
  if (cell.v !== undefined && cell.v !== null && cell.v !== '') {
    if (cell.v instanceof Date) return { text: cell.v.toISOString().slice(0, 10), num: true }
    if (cell.t === 'b') return { text: cell.v ? 'TRUE' : 'FALSE', num: false }
    return { text: String(cell.v), num }
  }
  if (cell.f) return { text: `=${cell.f}`, num: false }
  return { text: '', num: false }
}

// One sheet of a SheetJS-style workbook as the grid the table view draws:
// { name, rows, kinds, total_rows, total_cols }. rows holds text; kinds is a
// string per row with 'n' for a number and '.' for anything else, so the view
// can align numbers. At most maxRows rows and maxCols columns are kept; the
// totals say how much the sheet has. A sheet's range comes from its full
// reference when the reader cut the rows (sheetRows), else from its reference.
export function gridOfSheet(name, sheet, { maxRows = SHEET_MAX_ROWS, maxCols = SHEET_MAX_COLS } = {}) {
  const range = parseRange(sheet?.['!ref'])
  const full = parseRange(sheet?.['!fullref']) || range
  if (!range) return { name, rows: [], kinds: [], total_rows: 0, total_cols: 0 }
  const totalRows = full.e.r - full.s.r + 1
  const totalCols = full.e.c - full.s.c + 1
  const rows = []
  const kinds = []
  const nr = Math.min(range.e.r - range.s.r + 1, maxRows)
  const nc = Math.min(range.e.c - range.s.c + 1, maxCols)
  for (let r = 0; r < nr; r++) {
    const row = []
    let kind = ''
    for (let c = 0; c < nc; c++) {
      const { text, num } = cellText(sheet[cellRef(range.s.r + r, range.s.c + c)])
      row.push(text)
      kind += num ? 'n' : '.'
    }
    rows.push(row)
    kinds.push(kind)
  }
  return { name, rows, kinds, total_rows: totalRows, total_cols: totalCols }
}

// The table view's data for a whole workbook (SheetNames and Sheets, as
// SheetJS reads them).
export function gridOfWorkbook(wb, limits) {
  return { kind: 'table', sheets: (wb?.SheetNames || []).map(n => gridOfSheet(n, wb.Sheets?.[n], limits)) }
}
