// Helpers of the Gmail page. No React, so a node test drives them.

export const MAX_LABELS = 50
// The entry that lets a connection use the whole mailbox.
export const ALL = 'ALL'
const MAX_ENTRY = 128

// The label of one entry: a label name or ID as typed. A label name with
// spaces is written with dashes, because the host keeps no space in an entry.
// The case is kept; the add-on compares without regard to case.
export function labelKey(entry) {
  return String(entry || '').trim().replace(/\s+/g, '-')
}

// The labels of a text box: one per line, or separated by commas. Blank
// entries and repeats (without regard to case) are dropped.
export function parseLabels(text) {
  const seen = new Set()
  const out = []
  for (const part of String(text || '').split(/[\n\r,]+/)) {
    const key = labelKey(part)
    if (!key || seen.has(key.toLowerCase())) continue
    seen.add(key.toLowerCase())
    out.push(key)
  }
  return out
}

// eslint-disable-next-line no-control-regex -- a control character in an entry is the thing to refuse
const BAD = /[\u0000-\u001f\u007f<>"]/

// The first thing wrong with the typed list: { key, entry } or null.
export function labelsIssue(text) {
  const list = parseLabels(text)
  if (list.length > MAX_LABELS) return { key: 'labelsMany', entry: '' }
  const bad = list.find((l) => l.length > MAX_ENTRY || BAD.test(l))
  return bad === undefined ? null : { key: 'labelsShape', entry: bad.length > 60 ? `${bad.slice(0, 57)}...` : bad }
}

export const labelsProblem = (text) => labelsIssue(text)?.key || ''

// True when the list gives the whole mailbox.
export const hasAll = (list) => (list || []).some((l) => String(l).toLowerCase() === ALL.toLowerCase())

// What the agent may change in Gmail with this connection. The host keeps it
// on the binding and enforces it: off runs no write tool, propose stores each
// draft and each send for you to confirm. Every change also needs the
// optional access that was allowed at login.
export const WRITE_MODES = ['off', 'propose', 'auto']
// The modes the page offers. Every write tool of this add-on waits for a
// person (the tools set approval always), so auto would be the same as
// propose; a connection that has it keeps it and reads as asking first.
export const OFFERED_MODES = ['off', 'propose']
export const writeModeOf = (m) => (WRITE_MODES.includes(m) ? m : 'off')

// The fields this page adds to a binding: its form values, the request body
// they make, and whether they allow saving.
export const extraForm = (c) => ({ labels_text: (c.resources || []).join('\n'), write_mode: writeModeOf(c.write_mode) })
export const extraBody = (form) => ({ resources: parseLabels(form.labels_text), write_mode: writeModeOf(form.write_mode) })
export const extraValid = (form) => !labelsProblem(form.labels_text)
