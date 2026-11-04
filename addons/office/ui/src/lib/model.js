// What a preview draws, made from the answer of a read tool. The answer is
// data from the add-on: its text is only ever drawn as text. Anything that is
// not the shape a tool returns gives an error and no blocks.
const KINDS = new Set(['docx', 'xlsx', 'pptx'])
const MAX_BLOCKS = 400

const text = (v) => (typeof v === 'string' ? v : '')
const rowsOf = (v) => (Array.isArray(v) ? v.filter(Array.isArray).map(r => r.map(c => text(c))) : [])

const fail = (error) => ({ error, blocks: [], truncated: false })

export function modelOf(extension, payload) {
  if (!KINDS.has(extension)) return fail('unsupported')
  if (!payload || typeof payload !== 'object') return fail('unreadable')
  if (payload.ok === false) return fail(text(payload.error) || 'unreadable')
  if (payload.ok !== true) return fail('unreadable')
  const blocks = []
  if (extension === 'docx') {
    if (!Array.isArray(payload.paragraphs)) return fail('unreadable')
    for (const p of payload.paragraphs) {
      if (!p || typeof p !== 'object') continue
      const heading = /^Heading\d?$|^Title$/i.test(text(p.style))
      blocks.push({ type: heading ? 'heading' : 'paragraph', text: text(p.text) })
    }
    for (const rows of Array.isArray(payload.tables) ? payload.tables : []) blocks.push({ type: 'table', rows: rowsOf(rows) })
  } else if (extension === 'xlsx') {
    if (!Array.isArray(payload.sheets)) return fail('unreadable')
    for (const s of payload.sheets) {
      if (!s || typeof s !== 'object') continue
      blocks.push({ type: 'heading', text: text(s.name) })
      blocks.push({ type: 'table', rows: rowsOf(s.rows) })
    }
  } else {
    if (!Array.isArray(payload.slides)) return fail('unreadable')
    for (const s of payload.slides) {
      if (!s || typeof s !== 'object') continue
      const lines = []
      for (const sh of Array.isArray(s.shapes) ? s.shapes : []) {
        if (sh && Array.isArray(sh.text)) for (const l of sh.text) if (text(l) !== '') lines.push(text(l))
      }
      blocks.push({ type: 'slide', index: Number(s.index) || 0, title: text(s.title), lines })
    }
  }
  return { error: '', blocks: blocks.slice(0, MAX_BLOCKS), truncated: payload.truncated === true || blocks.length > MAX_BLOCKS }
}
