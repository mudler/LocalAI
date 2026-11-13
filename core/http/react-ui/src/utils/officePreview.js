// The decisions of the office previews of the preview pane: which files are
// drawn in the browser from their own bytes, when a file is too large for
// that, and how a workbook becomes a grid of text. No DOM, no network and no
// library here, so it runs in node; the drawing is in officeDocx.js,
// officeXlsx.js and officeSlides.js, which load on demand.

// The largest file the pane reads to draw it. A larger one keeps the server's
// text view (which has its own, smaller limit) and a Download.
export const OFFICE_SIZE_CAP = 25 * 1024 * 1024

// What a workbook view shows of a sheet. The rest is a note and Download.
export const SHEET_MAX_ROWS = 1000
export const SHEET_MAX_COLS = 50

const KINDS = [
  [/\.docx$/i, 'docx'],
  [/\.(xlsx|xlsm)$/i, 'xlsx'],
  [/\.pptx$/i, 'pptx'],
]

// docx, xlsx or pptx for a file the pane draws itself, else ''. Old binary
// formats (.doc, .xls, .ppt) and CSV are not drawn here.
export function officeKindOf(name) {
  for (const [re, kind] of KINDS) if (re.test(name || '')) return kind
  return ''
}

// What the pane does with a file item: { kind, draw, reason }. draw is true
// when the pane fetches the bytes and draws them. Otherwise the server's text
// view stays, and reason says why: 'tooLarge' (an item whose size is known to
// be over the cap), 'notFile' (a code block has no bytes), '' (not an office
// file at all).
export function officePlan(item) {
  const kind = item?.type === 'file' ? officeKindOf(item.name) : ''
  if (!kind) return { kind: '', draw: false, reason: item?.type === 'block' ? 'notFile' : '' }
  if ((item.size || 0) > OFFICE_SIZE_CAP) return { kind, draw: false, reason: 'tooLarge' }
  return { kind, draw: true, reason: '' }
}

// Whether the bytes that arrived are within the cap, by the header the server
// sent or the bytes themselves.
export function withinCap(length) {
  return Number.isFinite(length) && length <= OFFICE_SIZE_CAP
}

// Whether the pane draws the file itself: it does when the file is an office
// type within the cap and nothing has failed for this file yet. office is what
// the pane remembers of a failure ({ key, reason }); key names the file and its
// size, so a failure of one file never hides the drawing of another or of a
// new version of the same.
export function officeState(plan, office, key) {
  const failed = office?.key === key ? office.reason : plan.reason
  return { failed, drawn: plan.draw && !failed }
}

// Whether the bytes start like a zip archive, which every Office Open XML file
// is. The readers would take other bytes for text and draw nonsense (the
// spreadsheet reader reads any text as a table), so these never reach them.
export function isZip(buf) {
  const b = new Uint8Array(buf, 0, Math.min(4, buf?.byteLength || 0))
  return b.length === 4 && b[0] === 0x50 && b[1] === 0x4b && b[2] === 0x03 && b[3] === 0x04
}

// Reads the bytes of a file to draw them. Returns { buf } or { reason, error }
// where reason is 'tooLarge' (over the cap, by the header or by the bytes) or
// 'error' (the read failed). fetchFn is fetch, passed in so this runs in node.
export async function loadOfficeBytes(url, { fetchFn = fetch, signal } = {}) {
  try {
    const res = await fetchFn(url, { signal })
    if (!res.ok) return { reason: 'error', error: new Error(`HTTP ${res.status}`) }
    const length = Number(res.headers?.get?.('Content-Length'))
    if (length && !withinCap(length)) return { reason: 'tooLarge' }
    const buf = await res.arrayBuffer()
    if (!withinCap(buf.byteLength)) return { reason: 'tooLarge' }
    if (!isZip(buf)) return { reason: 'error', error: new Error('not an Office Open XML file') }
    return { buf }
  } catch (error) {
    return { reason: 'error', error }
  }
}
