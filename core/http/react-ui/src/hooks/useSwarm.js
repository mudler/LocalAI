import { useCallback, useEffect, useRef, useState } from 'react'
import { nodesApi } from '../utils/api'
import { usePolling } from './usePolling'

// The cluster roster, read the way the Nodes page always read it.
//
//   status  'loading' | 'ready' | 'single' | 'error'
//
// A single-node server never mounts the cluster routes, so /api/nodes answers
// 404 there (503 when they are mounted without a registry). Both mean "this is
// not a cluster", which is a state of the page and not an error. Any other
// failure keeps the last roster and reports `error`, so a blip does not blank
// the page.
export function useNodeList({ interval = 5000, enabled = true } = {}) {
  const [nodes, setNodes] = useState([])
  const [status, setStatus] = useState('loading')

  const read = useCallback(async () => {
    try {
      const data = await nodesApi.list()
      setNodes(Array.isArray(data) ? data : [])
      setStatus('ready')
    } catch (error) {
      const single = error.status === 404 || error.status === 503
        || error.message?.includes('503') || error.message?.includes('Service Unavailable')
      setStatus(single ? 'single' : 'error')
    }
  }, [])

  const { refetch } = usePolling(read, interval, { enabled })
  return { nodes, status, refetch }
}

// Every loaded replica in the cluster (GET /api/nodes/models). It is one
// controller query, so nothing reads it until `load()` is called, and it is read
// once and kept until `load(true)` asks again.
//
//   state  'idle' | 'loading' | 'loaded' | 'error'
export function useReplicas() {
  const [rows, setRows] = useState([])
  const [state, setState] = useState('idle')
  const [error, setError] = useState('')
  const started = useRef(false)

  const load = useCallback(async (force = false) => {
    if (started.current && !force) return
    started.current = true
    setState(current => (current === 'loaded' ? current : 'loading'))
    try {
      const data = await nodesApi.allModels()
      setRows(Array.isArray(data) ? data : [])
      setError('')
      setState('loaded')
    } catch (err) {
      setError(err.message || '')
      setState('error')
    }
  }, [])

  const retry = useCallback(() => { started.current = false; return load() }, [load])
  return { rows, state, error, load, retry }
}

// The placement rules (GET /api/nodes/scheduling). A failure reads as no rules:
// a single install has none, and nothing here should block a page on them.
export function useRules() {
  const [rules, setRules] = useState([])
  const [loaded, setLoaded] = useState(false)
  const read = useCallback(async () => {
    try {
      const data = await nodesApi.listScheduling()
      setRules(Array.isArray(data) ? data : [])
    } catch {
      setRules([])
    } finally {
      setLoaded(true)
    }
  }, [])
  useEffect(() => { read() }, [read])
  return { rules, loaded, refresh: read }
}

// True below the width where the map is not drawn and tables become cards.
export function usePhone(query = '(max-width: 640px)') {
  const [phone, setPhone] = useState(() => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(query).matches : false))
  useEffect(() => {
    if (!window.matchMedia) return undefined
    const list = window.matchMedia(query)
    const on = () => setPhone(list.matches)
    on()
    list.addEventListener('change', on)
    return () => list.removeEventListener('change', on)
  }, [query])
  return phone
}
