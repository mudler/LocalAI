// Clipboard helper that works in non-secure contexts.
//
// navigator.clipboard is only defined on https:// origins and on
// http://localhost. When LocalAI is served over plain http from a remote
// host (LXC + Docker is a common deployment), every page that called
// `navigator.clipboard.writeText` silently failed (#9904). This helper
// transparently falls back to a hidden-textarea + execCommand('copy')
// trick that browsers still honour when the page is not a secure context.
//
// Returns true on success, false on failure. Callers should use the return
// value to drive the success/failure toast — the old code always claimed
// success regardless of what actually happened.
export async function copyToClipboard(text) {
  if (text == null) return false
  const value = typeof text === 'string' ? text : String(text)

  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // Permissions denied, browser refused, etc. — try the fallback.
    }
  }

  return legacyCopy(value)
}

function legacyCopy(value) {
  if (typeof document === 'undefined') return false
  const ta = document.createElement('textarea')
  ta.value = value
  // Keep the textarea out of the viewport and out of layout reads. Using
  // `position: fixed` + a negative offset avoids scrolling the page when
  // we call .select() below.
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.top = '0'
  ta.style.left = '-9999px'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  // Preserve the current selection so triggering execCommand doesn't blow
  // away whatever the user had highlighted on the page.
  const previousSelection = saveSelection()
  let ok = false
  try {
    ta.select()
    ta.setSelectionRange(0, value.length)
    ok = document.execCommand('copy')
  } catch {
    ok = false
  } finally {
    document.body.removeChild(ta)
    restoreSelection(previousSelection)
  }
  return ok
}

function saveSelection() {
  try {
    const sel = window.getSelection()
    if (!sel || sel.rangeCount === 0) return null
    const ranges = []
    for (let i = 0; i < sel.rangeCount; i++) ranges.push(sel.getRangeAt(i).cloneRange())
    return ranges
  } catch {
    return null
  }
}

function restoreSelection(ranges) {
  if (!ranges) return
  try {
    const sel = window.getSelection()
    if (!sel) return
    sel.removeAllRanges()
    for (const r of ranges) sel.addRange(r)
  } catch {
    // best-effort
  }
}
