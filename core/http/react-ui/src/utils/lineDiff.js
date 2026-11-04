// A line diff for the change cards: what a proposal adds and removes, as the
// person reads it. Pure rules, so a node test drives them.

// Past this many lines on either side the exact diff costs too much for a
// card; the whole old text counts as removed and the new one as added.
const MAX_EXACT = 600

export function splitLines(text) {
  if (!text) return []
  const lines = String(text).replace(/\r\n/g, '\n').split('\n')
  if (lines[lines.length - 1] === '') lines.pop()
  return lines
}

// Returns [{ type: 'same' | 'add' | 'del', text }] in reading order.
export function diffLines(before, after) {
  const a = splitLines(before)
  const b = splitLines(after)
  // Trim what both ends share, so the table is small for a short edit.
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++
  const ma = a.slice(head, a.length - tail)
  const mb = b.slice(head, b.length - tail)
  const out = a.slice(0, head).map((text) => ({ type: 'same', text }))
  if (ma.length > MAX_EXACT || mb.length > MAX_EXACT) {
    out.push(...ma.map((text) => ({ type: 'del', text })), ...mb.map((text) => ({ type: 'add', text })))
  } else {
    // Longest common subsequence of the middle.
    const w = mb.length + 1
    const t = new Uint16Array((ma.length + 1) * w)
    for (let i = ma.length - 1; i >= 0; i--) {
      for (let j = mb.length - 1; j >= 0; j--) {
        t[i * w + j] = ma[i] === mb[j] ? t[(i + 1) * w + j + 1] + 1 : Math.max(t[(i + 1) * w + j], t[i * w + j + 1])
      }
    }
    let i = 0
    let j = 0
    while (i < ma.length && j < mb.length) {
      if (ma[i] === mb[j]) { out.push({ type: 'same', text: ma[i] }); i++; j++ }
      else if (t[(i + 1) * w + j] >= t[i * w + j + 1]) { out.push({ type: 'del', text: ma[i] }); i++ }
      else { out.push({ type: 'add', text: mb[j] }); j++ }
    }
    for (; i < ma.length; i++) out.push({ type: 'del', text: ma[i] })
    for (; j < mb.length; j++) out.push({ type: 'add', text: mb[j] })
  }
  out.push(...a.slice(a.length - tail).map((text) => ({ type: 'same', text })))
  return out
}

// How many lines a diff adds and removes.
export function diffStats(ops) {
  let added = 0
  let removed = 0
  for (const op of ops) {
    if (op.type === 'add') added++
    else if (op.type === 'del') removed++
  }
  return { added, removed }
}

// Keeps the changed lines and `context` unchanged lines around them; a longer
// run of unchanged lines becomes { type: 'gap', count }.
export function withContext(ops, context = 2) {
  const keep = new Array(ops.length).fill(false)
  ops.forEach((op, i) => {
    if (op.type === 'same') return
    for (let k = Math.max(0, i - context); k <= Math.min(ops.length - 1, i + context); k++) keep[k] = true
  })
  const out = []
  let skipped = 0
  ops.forEach((op, i) => {
    if (keep[i]) {
      if (skipped) { out.push({ type: 'gap', count: skipped }); skipped = 0 }
      out.push(op)
    } else {
      skipped++
    }
  })
  if (skipped) out.push({ type: 'gap', count: skipped })
  return out
}
