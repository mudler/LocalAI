// The calls and the local record that the identity views share. Ported from
// components/identity/useRegistry.js and the voiceApi/faceApi helpers of
// utils/api.js. Calls go to the core routes (/v1/voice/*, /v1/face/*) with the
// signed-in user's own session, through ctx.core.fetch.
import { loadList, missingFromServer, saveList } from './identity.js'

// A failed call keeps its status and parsed body, the way utils/api.js
// handleResponse does: the registry reads status 404, explainError reads the
// status and the message.
async function post(ctx, path, body) {
  const response = await ctx.core.fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!response.ok) {
    let message = `HTTP ${response.status}`
    let errorBody = null
    try {
      errorBody = await response.json()
      if (errorBody?.error?.message) message = errorBody.error.message
      else if (errorBody?.error) message = errorBody.error
    } catch { /* the answer was not JSON */ }
    const err = new Error(message)
    err.status = response.status
    err.body = errorBody
    throw err
  }
  // forget answers 204 with no body; the core helper hands back the response
  // for anything that is not JSON, and nothing here reads it.
  const type = response.headers?.get?.('content-type')
  if (response.status === 204 || (type && !type.includes('application/json'))) return null
  return response.json()
}

const CALLS = ['verify', 'analyze', 'embed', 'register', 'identify', 'forget']

// { voice: { verify, analyze, embed, register, identify, forget }, face: {...} }
export function createIdentityApi(ctx) {
  const forKind = (kind) => Object.fromEntries(CALLS.map((call) => [call, (body) => post(ctx, `/v1/${kind}/${call}`, body)]))
  return { voice: forKind('voice'), face: forKind('face') }
}

export function createIdentityRegistry(ctx) {
  const { useCallback, useEffect, useMemo, useState } = ctx.React

  // The people this browser enrolled, plus what the last search learned about
  // the server. The server has no list call, so the list is a local record: it
  // can be out of date after a restart, and the only evidence is a search that
  // asked for everyone and did not get someone back.
  //
  //   storageKey   localStorage key of the saved list
  //   forget       the real call, `({ id }) => Promise`
  //   onForgot     (entry, outcome) after the call: 'gone', 'already-gone' or an Error
  function useRegistry({ storageKey, forget, onForgot }) {
    const [entries, setEntries] = useState(() => loadList(storageKey))
    const [search, setSearch] = useState(null)
    const [latest, setLatest] = useState(null)

    useEffect(() => { saveList(storageKey, entries) }, [storageKey, entries])

    const run = useCallback(async (_key, entry) => {
      try {
        await forget({ id: entry.id })
        setEntries(prev => prev.filter(e => e.id !== entry.id))
        setSearch(prev => (prev ? { ...prev, extras: prev.extras.filter(e => e.id !== entry.id) } : prev))
        onForgot?.(entry, 'gone')
      } catch (err) {
        if (err?.status === 404) {
          setEntries(prev => prev.filter(e => e.id !== entry.id))
          onForgot?.(entry, 'already-gone')
        } else {
          onForgot?.(entry, err)
        }
      }
    }, [forget, onForgot])

    const forgetting = ctx.hooks.useDelayedAction(run)
    const waiting = forgetting.waiting || {}

    const add = useCallback((entry) => setEntries(prev => [entry, ...prev.filter(e => e.id !== entry.id)]), [])

    // Swap a row for the same person under the id the server just made.
    const replace = useCallback((oldId, entry) => setEntries(prev => prev.map(e => (e.id === oldId ? entry : e))), [])

    const schedule = useCallback((entry) => {
      setLatest(entry)
      forgetting.schedule(entry.id, entry)
    }, [forgetting.schedule]) // eslint-disable-line react-hooks/exhaustive-deps

    const undo = useCallback((id) => {
      forgetting.cancel?.(id)
      setLatest(prev => (prev?.id === id ? null : prev))
    }, [forgetting.cancel]) // eslint-disable-line react-hooks/exhaustive-deps

    const finish = useCallback((id) => {
      forgetting.flush?.(id)
      setLatest(prev => (prev?.id === id ? null : prev))
    }, [forgetting.flush]) // eslint-disable-line react-hooks/exhaustive-deps

    // Record what a search returned. `asked` is the top_k it sent.
    const noteSearch = useCallback((matches, asked) => {
      const ids = matches.map(m => m.id)
      const known = new Set(entries.map(e => e.id))
      setSearch({
        ids,
        complete: matches.length < asked,
        extras: matches.filter(m => !known.has(m.id)).map(m => ({ id: m.id, name: m.name || m.id, labels: m.labels || {} })),
      })
    }, [entries])

    const missing = useMemo(() => missingFromServer(entries, search), [entries, search])

    return {
      entries, add, replace, missing, searched: !!search, extras: search?.extras || [],
      waiting, latest: latest && waiting[latest.id] ? latest : null,
      schedule, undo, finish, noteSearch,
    }
  }

  return { useRegistry }
}
