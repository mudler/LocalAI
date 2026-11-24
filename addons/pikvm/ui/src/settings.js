// The model of the settings editor. No React, so a node test drives it. The
// add-on is the authority; these helpers only show a problem next to the field.

export const DEFAULT_CHARS = 200
export const MAX_CHARS = 1000

export const draftOf = (d) => ({
  allowTyping: d?.allowTyping === true,
  allowHardPower: d?.allowHardPower === true,
  maxTypeChars: String(d?.maxTypeChars || DEFAULT_CHARS),
})

export const same = (a, b) => a.allowTyping === b.allowTyping && a.allowHardPower === b.allowHardPower && a.maxTypeChars === b.maxTypeChars

// A key of the page's words for a problem in the draft, or ''.
export function draftProblem(d) {
  const n = Number(d.maxTypeChars)
  if (!/^\d+$/.test(String(d.maxTypeChars)) || n < 1 || n > MAX_CHARS) return 'settings.maxCharsShape'
  return ''
}

// The fields to send.
export const valuesOf = (d) => ({ allowTyping: d.allowTyping, allowHardPower: d.allowHardPower, maxTypeChars: Number(d.maxTypeChars) })
