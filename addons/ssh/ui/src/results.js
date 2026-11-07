// The results of the Test button, kept by connection so the row can draw them.
// The shared page only shows one line of text at the top; the Test hook of
// this page writes here, and the row reads. No React: a node test drives it.

const results = new Map()
const listeners = new Set()
const emit = () => { for (const f of [...listeners]) f() }

// Marks a connection as being tested and drops its old result.
export function startTest(id) {
  results.set(id, { busy: true, items: [], at: 0 })
  emit()
}

// Stores the result of a finished test: items are { tone, text }.
export function finishTest(id, items, at = Date.now()) {
  results.set(id, { busy: false, items, at })
  emit()
}

export const resultOf = (id) => results.get(id) || null

export function forget(id) {
  if (results.delete(id)) emit()
}

export function subscribe(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}
