import { useCallback, useEffect, useReducer, useRef } from 'react'
import { initialLoad, loadReducer } from '../utils/loadState'

// Loads the data of one region and returns { status, data, error, retry,
// reload }. status is 'loading', 'ready' or 'error'. Render <LoadError> for
// 'error' and pass `retry` to it; render the empty state only when status is
// 'ready' and the data is empty.
//
//   const agents = useLoad(() => agentsApi.list(), [])
//
// `reload` loads again in the background and keeps the current data on
// screen; `retry` starts over from the loading state.
export default function useLoad(loader, deps = []) {
  const [state, dispatch] = useReducer(loadReducer, initialLoad)
  const counter = useRef(0)
  const loaderRef = useRef(loader)
  loaderRef.current = loader

  const run = useCallback((keep = false) => {
    const request = ++counter.current
    dispatch({ type: 'start', request, keep })
    return Promise.resolve()
      .then(() => loaderRef.current())
      .then(
        data => dispatch({ type: 'success', request, data }),
        error => dispatch({ type: 'failure', request, error }),
      )
  }, [])

  useEffect(() => {
    run()
    // The caller lists what the loader depends on, as with useEffect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  const retry = useCallback(() => run(false), [run])
  const reload = useCallback(() => run(true), [run])
  return { status: state.status, data: state.data, error: state.error, refreshing: !!state.refreshing, retry, reload }
}
