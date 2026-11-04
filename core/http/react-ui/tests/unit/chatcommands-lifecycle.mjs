// Executes source hooks with persistent slots and explicitly controlled effect flushes.
// Keeping effects separate lets tests inspect the render before cleanup runs.
import { readFileSync } from 'node:fs'

export function hookHarness(file, name, imports, transform = source => source) {
  const slots = []
  let cursor = 0
  let pending = new Map()
  // Layout effects run at commit, before the render returns to its caller and
  // before any passive effect. A discarded render never commits them.
  let layout = new Map()
  const same = (a, b) => a && b && a.length === b.length && a.every((v, i) => Object.is(v, b[i]))
  const hooks = {
    useState(initial) {
      const i = cursor++
      if (!(i in slots)) slots[i] = typeof initial === 'function' ? initial() : initial
      return [slots[i], value => { slots[i] = typeof value === 'function' ? value(slots[i]) : value }]
    },
    useRef(initial) {
      const i = cursor++
      return slots[i] ||= { current: initial }
    },
    useEffect(fn, deps) {
      const i = cursor++
      if (!same(slots[i]?.deps, deps)) pending.set(i, () => {
        slots[i]?.cleanup?.()
        slots[i] = { deps, cleanup: fn() }
      })
    },
    useLayoutEffect(fn, deps) {
      const i = cursor++
      if (!same(slots[i]?.deps, deps)) layout.set(i, () => {
        slots[i]?.cleanup?.()
        slots[i] = { deps, cleanup: fn() }
      })
    },
    useMemo(fn, deps) {
      const i = cursor++
      if (!same(slots[i]?.deps, deps)) slots[i] = { deps, value: fn() }
      return slots[i].value
    },
    useSyncExternalStore(subscribe, snapshot) { cursor++; return snapshot() },
    useCallback(fn) { cursor++; return fn },
  }
  const source = transform(readFileSync(new URL(file, import.meta.url), 'utf8')
    .replace(/^import .*\n/gm, '').replace(/^export \{.*\}\n/gm, '').replace(/export function/g, 'function'))
  const scope = { ...hooks, ...imports }
  const hook = new Function(...Object.keys(scope), `${source}; return ${name}`)(...Object.values(scope))
  return {
    render(...args) {
      cursor = 0
      const out = hook(...args)
      const effects = layout; layout = new Map(); effects.forEach(fn => fn())
      return out
    },
    // A render that React throws away: nothing is committed, so no layout effect runs.
    renderDiscarded(...args) { cursor = 0; const out = hook(...args); layout = new Map(); return out },
    unmount() { pending.clear(); slots.forEach(slot => slot?.cleanup?.()) },
    flush() { const effects = pending; pending = new Map(); effects.forEach(fn => fn()) },
  }
}
