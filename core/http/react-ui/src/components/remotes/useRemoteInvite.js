import { useCallback, useMemo, useState } from 'react'
import { p2pNetApi, remotesApi, settingsApi } from '../../utils/api'
import { inviteFailure, inviteProblem, parseInviteLink, shortPeer } from '../../utils/p2p'
import { MAX_ADOPT, chosenModels, createErrorKind, initialTicked, mergeAdopt, missingModels, resultParts } from '../../utils/remotes'

// "Connect a remote" with an invite link from another LocalAI. The link is
// single use, so Connect redeems it and saves the remote at once, with no
// model yet: the models the remote lists come back in `remote.new`. The
// choose step then adopts the ticked ones. The object has the shape that
// RemoteChooseStep reads from useRemoteConnect, so both ways share that step.
//
// P2P must be on to dial the other server. When it is off, Connect turns it
// on in mode "connect" first: this server then dials out and accepts nobody,
// which needs no authentication.
export function useRemoteInvite({ onDone } = {}) {
  const [link, setLinkState] = useState('')
  // idle | connecting | ok | failed
  const [phase, setPhase] = useState('idle')
  const [failure, setFailure] = useState(null)
  const [switched, setSwitched] = useState(false)
  // The saved remote: the answer of the create call.
  const [saved, setSaved] = useState(null)
  const [ticked, setTicked] = useState([])
  const [name, setNameState] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState(null)
  const [outcome, setOutcome] = useState(null)
  const [adopting, setAdopting] = useState(false)

  const parsed = useMemo(() => parseInviteLink(link), [link])
  const problem = inviteProblem(parsed)
  const busy = phase === 'connecting'
  const models = useMemo(() => (Array.isArray(saved?.remote?.new) ? saved.remote.new : []), [saved])
  const remoteName = saved?.remote?.name || ''
  const finalName = (name || remoteName).trim()
  const locked = outcome !== null

  const setLink = useCallback((next) => {
    if (busy || saved) return
    setLinkState(next)
    setFailure(null)
    if (phase === 'failed') setPhase('idle')
  }, [busy, saved, phase])

  const connect = useCallback(async () => {
    if (!parsed.ok || problem || busy || saved) return
    setPhase('connecting'); setFailure(null)
    try {
      const st = await p2pNetApi.status().catch(() => null)
      if (st && st.mode === 'off') {
        await settingsApi.save({ p2p_mode: 'connect' })
        setSwitched(true)
      }
      const label = parsed.link.label || `LocalAI ${shortPeer(parsed.link.peer)}`
      // The step warns about an open relay before Connect, so Connect accepts it.
      const openRelay = parsed.link.relays.some(r => r.open)
      const res = await remotesApi.create({
        kind: 'localai', name: label, invite: link.trim(), ...(openRelay ? { allow_open_relay: true } : {}),
      })
      const list = Array.isArray(res?.remote?.new) ? res.remote.new : []
      setSaved(res)
      setTicked(initialTicked('localai', list))
      setPhase('ok')
    } catch (err) {
      setFailure({ kind: inviteFailure(err), message: err?.message || '' })
      setPhase('failed')
    }
  }, [parsed, problem, busy, saved, link])

  const toggle = useCallback((id) => {
    if (locked) return
    setTicked(list => (list.includes(id) ? list.filter(x => x !== id) : [...list, id]))
  }, [locked])
  const setMany = useCallback((ids, on) => {
    if (locked) return
    setTicked(list => {
      const set = new Set(list)
      for (const id of ids) { if (on) set.add(id); else set.delete(id) }
      return [...set]
    })
  }, [locked])
  const setName = useCallback((next) => { if (!locked) { setNameState(next); setCreateError(null) } }, [locked])

  const chosen = chosenModels(models, ticked, 'localai')

  const settle = useCallback((result) => {
    const { skipped, warning } = resultParts(result)
    setOutcome(result)
    if (!warning && skipped.length === 0) onDone?.(result)
  }, [onDone])

  // Adds the ticked models to the remote saved by Connect, after a rename
  // when the person typed another name.
  const create = useCallback(async () => {
    const id = saved?.remote?.id
    if (!id || outcome) return null
    setCreating(true); setCreateError(null)
    try {
      if (finalName && finalName !== remoteName) await remotesApi.update(id, { name: finalName })
      const res = await remotesApi.adopt(id, chosen)
      const result = { ...res, remote: { ...(res?.remote || saved.remote), name: finalName || remoteName } }
      settle(result)
      return result
    } catch (err) {
      setCreateError({ kind: createErrorKind(err), message: err?.message || '' })
      return null
    } finally {
      setCreating(false)
    }
  }, [saved, outcome, finalName, remoteName, chosen, settle])

  const adopt = useCallback(async () => {
    const id = outcome?.remote?.id
    if (!id) return null
    const missing = missingModels(chosen, outcome)
    if (missing.length === 0) return null
    setAdopting(true); setCreateError(null)
    try {
      const merged = mergeAdopt(outcome, await remotesApi.adopt(id, missing))
      settle(merged)
      return merged
    } catch (err) {
      setCreateError({ kind: createErrorKind(err), message: err?.message || '' })
      return null
    } finally {
      setAdopting(false)
    }
  }, [outcome, chosen, settle])

  const finish = useCallback(() => { if (outcome) onDone?.(outcome) }, [outcome, onDone])

  return {
    kind: 'localai', link, setLink, parsed, problem, connect, failure, switched, saved,
    phase, models, ticked, chosen, name: finalName, nameValue: name, setName,
    creating, createError, outcome, locked, adopting, tooMany: ticked.length > MAX_ADOPT,
    toggle, setMany, create, adopt, finish,
  }
}
