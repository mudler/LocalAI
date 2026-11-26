// SPDX-License-Identifier: MIT
// The phone layout switch. The launcher renderer is shared with the desktop,
// so mobile.css applies only under html.lnr-mobile, which this module sets.
// It also sets html.lnr-ime while the soft keyboard is open, so the status
// bar can step aside (it would otherwise sit on top of the keyboard).

export const MOBILE_CLASS = 'lnr-mobile'
export const IME_CLASS = 'lnr-ime'

/** Height of the status bar on a phone in CSS pixels (dp), without the bottom system inset. mobile.css draws the same. */
export const MOBILE_BAR_HEIGHT = 48

// A keyboard is taller than this; a browser toolbar or a status line that
// hides is shorter.
const IME_MIN_HEIGHT = 150

/**
 * Calls `cb` with true when installMobileShell marks the keyboard as open
 * and false when it clears the mark. The native WebUI view must know too:
 * while the keyboard hides the bar, it keeps no room for it.
 */
export function onKeyboardChange(win: Window, cb: (open: boolean) => void): () => void {
  const root = win.document.documentElement
  let open = root.classList.contains(IME_CLASS)
  const mo = new MutationObserver(() => {
    const now = root.classList.contains(IME_CLASS)
    if (now === open) return
    open = now
    cb(now)
  })
  mo.observe(root, { attributes: true, attributeFilter: ['class'] })
  return () => mo.disconnect()
}

/**
 * Marks the document for the phone layout and follows the soft keyboard.
 * Returns a function that undoes both.
 */
export function installMobileShell(win: Window): () => void {
  const root = win.document.documentElement
  root.classList.add(MOBILE_CLASS)
  const vv = win.visualViewport
  if (!vv) {
    return () => root.classList.remove(MOBILE_CLASS)
  }
  // The full height for the current width. With adjustResize the layout
  // viewport shrinks with the keyboard too, so the window height is no
  // reference: keep the tallest height seen since the last rotation.
  let width = vv.width
  let full = vv.height
  const update = () => {
    if (vv.width !== width) {
      width = vv.width
      full = vv.height
    }
    full = Math.max(full, vv.height)
    root.classList.toggle(IME_CLASS, full - vv.height >= IME_MIN_HEIGHT)
  }
  vv.addEventListener('resize', update)
  return () => {
    vv.removeEventListener('resize', update)
    root.classList.remove(MOBILE_CLASS, IME_CLASS)
  }
}
