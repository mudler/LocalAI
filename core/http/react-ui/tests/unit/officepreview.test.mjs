import test from 'node:test'
import assert from 'node:assert/strict'
import {
  OFFICE_SIZE_CAP, SHEET_MAX_COLS, SHEET_MAX_ROWS, officeKindOf, officePlan, officeState, loadOfficeBytes, withinCap, isZip,
} from '../../src/utils/officePreview.js'
import { cellRef, cellText, gridOfSheet, gridOfWorkbook, parseCellRef, parseRange } from '../../src/utils/officeGrid.js'
import { viewOf } from '../../src/utils/previewPane.js'

const file = (name, size = 1000) => ({ type: 'file', path: `/Docs/${name}`, name, size })

test('office preview: only docx, xlsx, xlsm and pptx are drawn in the browser', () => {
  const kinds = {
    'r.docx': 'docx', 'R.DOCX': 'docx', 'b.xlsx': 'xlsx', 'm.xlsm': 'xlsx', 'd.pptx': 'pptx',
    'old.doc': '', 'old.xls': '', 'old.ppt': '', 'q.csv': '', 'q.tsv': '', 'n.txt': '', 'docx': '', '': '',
  }
  for (const [name, kind] of Object.entries(kinds)) assert.equal(officeKindOf(name), kind, name)
  // The text views stay the fallback: the view of each file is unchanged.
  assert.equal(viewOf('r.docx'), 'document')
  assert.equal(viewOf('b.xlsx'), 'table')
  assert.equal(viewOf('d.pptx'), 'slides')
})

test('office preview: a file within the cap is drawn; a code block and other files never are', () => {
  assert.deepEqual(officePlan(file('r.docx')), { kind: 'docx', draw: true, reason: '' })
  assert.deepEqual(officePlan(file('b.xlsx', OFFICE_SIZE_CAP)), { kind: 'xlsx', draw: true, reason: '' })
  assert.deepEqual(officePlan({ type: 'file', path: '/x.pptx', name: 'x.pptx' }), { kind: 'pptx', draw: true, reason: '' })
  assert.deepEqual(officePlan(file('q.csv')), { kind: '', draw: false, reason: '' })
  assert.deepEqual(officePlan({ type: 'block', name: 'a.docx', code: 'x' }), { kind: '', draw: false, reason: 'notFile' })
})

test('office preview: a file over the size cap falls back to the text view with a reason', () => {
  const plan = officePlan(file('big.docx', OFFICE_SIZE_CAP + 1))
  assert.deepEqual(plan, { kind: 'docx', draw: false, reason: 'tooLarge' })
  assert.deepEqual(officeState(plan, { key: '', reason: '' }, 'k'), { failed: 'tooLarge', drawn: false })
  assert.ok(withinCap(OFFICE_SIZE_CAP))
  assert.ok(!withinCap(OFFICE_SIZE_CAP + 1))
  assert.ok(!withinCap(NaN))
})

test('office preview: a failed drawing falls back for that file only', () => {
  const plan = officePlan(file('r.docx'))
  assert.deepEqual(officeState(plan, { key: '', reason: '' }, '/Docs/r.docx:1000'), { failed: '', drawn: true })
  const failed = { key: '/Docs/r.docx:1000', reason: 'error' }
  assert.deepEqual(officeState(plan, failed, '/Docs/r.docx:1000'), { failed: 'error', drawn: false })
  // Another file, or a new version of the same one, is drawn again.
  assert.equal(officeState(plan, failed, '/Docs/other.docx:1000').drawn, true)
  assert.equal(officeState(plan, failed, '/Docs/r.docx:2000').drawn, true)
})

const zipBytes = () => new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0, 0, 0, 0]).buffer
const reply = (over = {}) => ({
  ok: true, status: 200, headers: { get: () => null }, arrayBuffer: async () => zipBytes(), ...over,
})

test('office preview: the bytes load, and a failed read, a refused read or a too large file say why', async () => {
  const ok = await loadOfficeBytes('/f', { fetchFn: async () => reply() })
  assert.equal(ok.buf.byteLength, 8)

  const status = await loadOfficeBytes('/f', { fetchFn: async () => reply({ ok: false, status: 404 }) })
  assert.equal(status.reason, 'error')
  assert.match(status.error.message, /404/)

  const network = await loadOfficeBytes('/f', { fetchFn: async () => { throw new Error('offline') } })
  assert.equal(network.reason, 'error')

  const byHeader = await loadOfficeBytes('/f', {
    fetchFn: async () => reply({ headers: { get: () => String(OFFICE_SIZE_CAP + 1) }, arrayBuffer: async () => { throw new Error('not read') } }),
  })
  assert.deepEqual(byHeader, { reason: 'tooLarge' })

  // Bytes that are not a zip archive never reach a reader, which would take
  // them for text.
  const notZip = await loadOfficeBytes('/f', { fetchFn: async () => reply({ arrayBuffer: async () => new TextEncoder().encode('bytes').buffer }) })
  assert.equal(notZip.reason, 'error')
  assert.ok(isZip(zipBytes()))
  assert.ok(!isZip(new ArrayBuffer(2)))
  assert.ok(!isZip(undefined))

  const byBytes = await loadOfficeBytes('/f', { fetchFn: async () => reply({ arrayBuffer: async () => ({ byteLength: OFFICE_SIZE_CAP + 1 }) }) })
  assert.deepEqual(byBytes, { reason: 'tooLarge' })
})

test('office grid: cell addresses and ranges', () => {
  assert.deepEqual(parseCellRef('A1'), { r: 0, c: 0 })
  assert.deepEqual(parseCellRef('$AA10'), { r: 9, c: 26 })
  assert.equal(parseCellRef('nope'), null)
  assert.equal(cellRef(0, 0), 'A1')
  assert.equal(cellRef(9, 26), 'AA10')
  assert.deepEqual(parseRange('B2:D5'), { s: { r: 1, c: 1 }, e: { r: 4, c: 3 } })
  assert.deepEqual(parseRange('C3'), { s: { r: 2, c: 2 }, e: { r: 2, c: 2 } })
  assert.equal(parseRange(''), null)
})

test('office grid: a cell shows its formatted text, else its value, else its formula', () => {
  assert.deepEqual(cellText({ t: 'n', v: 12000, w: '$12,000.00' }), { text: '$12,000.00', num: true })
  assert.deepEqual(cellText({ t: 's', v: 'Laptops' }), { text: 'Laptops', num: false })
  assert.deepEqual(cellText({ t: 'b', v: true }), { text: 'TRUE', num: false })
  assert.deepEqual(cellText({ t: 'd', v: new Date('2026-01-15T00:00:00Z') }), { text: '2026-01-15', num: true })
  // A file written by a script has formulas without stored results.
  assert.deepEqual(cellText({ t: 'z', f: 'C2+D2', v: 0 }), { text: '=C2+D2', num: false })
  assert.deepEqual(cellText({ t: 'n', f: 'SUM(A1:A3)' }), { text: '=SUM(A1:A3)', num: false })
  assert.deepEqual(cellText({ t: 'z' }), { text: '', num: false })
  assert.deepEqual(cellText(undefined), { text: '', num: false })
})

test('office grid: a sheet is capped to its rows and columns, with the totals kept', () => {
  const sheet = { '!ref': 'A1:C3', A1: { t: 's', v: 'Item' }, B1: { t: 's', v: 'Qty' }, A2: { t: 's', v: 'Pen' }, B2: { t: 'n', v: 3, w: '3' } }
  const g = gridOfSheet('S', sheet)
  assert.deepEqual(g.rows, [['Item', 'Qty', ''], ['Pen', '3', ''], ['', '', '']])
  assert.deepEqual(g.kinds, ['...', '.n.', '...'])
  assert.equal(g.total_rows, 3)
  assert.equal(g.total_cols, 3)

  const capped = gridOfSheet('S', sheet, { maxRows: 2, maxCols: 1 })
  assert.deepEqual(capped.rows, [['Item'], ['Pen']])
  assert.equal(capped.total_rows, 3)
  assert.equal(capped.total_cols, 3)

  // The reader cut the rows (sheetRows); the full range keeps the totals.
  const cut = gridOfSheet('S', { '!ref': 'A1:B2', '!fullref': 'A1:B5000', A1: { t: 's', v: 'x' } })
  assert.equal(cut.total_rows, 5000)
  assert.equal(cut.rows.length, 2)

  // A sheet without a range is empty, not an error.
  assert.deepEqual(gridOfSheet('E', {}), { name: 'E', rows: [], kinds: [], total_rows: 0, total_cols: 0 })
  assert.ok(SHEET_MAX_ROWS > 0 && SHEET_MAX_COLS > 0)
})

test('office grid: a workbook keeps its sheets in order and tolerates a missing sheet', () => {
  const wb = { SheetNames: ['One', 'Two', 'Gone'], Sheets: { One: { '!ref': 'A1', A1: { t: 's', v: 'a' } }, Two: { '!ref': 'A1', A1: { t: 'n', v: 1, w: '1' } } } }
  const g = gridOfWorkbook(wb)
  assert.equal(g.kind, 'table')
  assert.deepEqual(g.sheets.map(s => s.name), ['One', 'Two', 'Gone'])
  assert.deepEqual(g.sheets[2].rows, [])
  assert.deepEqual(gridOfWorkbook(null).sheets, [])
})
