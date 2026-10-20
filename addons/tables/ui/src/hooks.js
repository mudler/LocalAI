// Small hooks over the host's React. They are plain functions of ctx so no
// module holds a React of its own.

// useLoader runs `load` when its inputs change and keeps the newest answer
// only: a slow answer of an old request never replaces a newer one, and
// nothing is set after unmount.
//   state: 'loading' | 'ready' | 'error'
export function createHooks(ctx) {
  const { R } = ctx
  function useLoader(load, deps) {
    const [view, setView] = R.useState({ state: 'loading', data: null, error: null })
    const turn = R.useRef(0)
    const mounted = R.useRef(true)
    const loadRef = R.useRef(load)
    loadRef.current = load
    R.useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
    const run = R.useCallback((quiet) => {
      const mine = ++turn.current
      if (!quiet) setView(v => ({ state: 'loading', data: v.data, error: null }))
      return Promise.resolve()
        .then(() => loadRef.current())
        .then(
          (data) => { if (mounted.current && mine === turn.current) setView({ state: 'ready', data, error: null }) },
          (error) => { if (mounted.current && mine === turn.current) setView(v => ({ state: 'error', data: v.data, error })) },
        )
    }, [])
    R.useEffect(() => { run(false) }, [run, ...deps])
    return { ...view, reload: () => run(false), refresh: () => run(true) }
  }

  // useDebounced returns `value` once it stopped changing for `ms`.
  function useDebounced(value, ms) {
    const [out, setOut] = R.useState(value)
    R.useEffect(() => {
      const id = setTimeout(() => setOut(value), ms)
      return () => clearTimeout(id)
    }, [value, ms])
    return out
  }

  // useOperation keeps one operation identifier across the retries of one
  // submission, so a lost answer is repeated, never applied twice, and takes
  // a new one after a definite outcome.
  function useOperation() {
    const id = R.useRef('')
    return {
      current() {
        if (!id.current) id.current = `op-${(globalThis.crypto && globalThis.crypto.randomUUID) ? globalThis.crypto.randomUUID() : Math.random().toString(16).slice(2)}`
        return id.current
      },
      settle() { id.current = '' },
    }
  }

  return { useLoader, useDebounced, useOperation }
}
