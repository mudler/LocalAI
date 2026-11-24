// The pure rules of <Segmented mode="radio">, so the node unit tests drive them
// without a browser. A radio group is one tab stop: the checked option holds
// it (the first enabled one when nothing is checked), and the arrow keys move
// the choice, skipping disabled options and wrapping at both ends.

const enabled = (options) => options.map((o, i) => (o.disabled ? -1 : i)).filter(i => i >= 0)

// The index of the option that is reachable with Tab, or -1 when none is enabled.
export function radioTabStop(options, value) {
  const checked = options.findIndex(o => o.id === value && !o.disabled)
  return checked >= 0 ? checked : (enabled(options)[0] ?? -1)
}

// The index a key moves the choice to, or -1 when the key does nothing here.
// `from` is the index of the focused option.
export function radioKeyTarget(options, from, key) {
  const ok = enabled(options)
  if (ok.length === 0) return -1
  if (key === 'Home') return ok[0]
  if (key === 'End') return ok[ok.length - 1]
  const step = key === 'ArrowRight' || key === 'ArrowDown' ? 1 : key === 'ArrowLeft' || key === 'ArrowUp' ? -1 : 0
  if (step === 0) return -1
  const n = options.length
  for (let k = 1; k <= n; k++) {
    const i = (((from + step * k) % n) + n) % n
    if (!options[i].disabled) return i
  }
  return -1
}
