import { useCallback, useState } from 'react'
import { remotesApi } from '../../utils/api'
import {
  MAX_ADOPT, PRESETS, canTest, chosenModels, createErrorKind, defaultName, failStep, initialTicked,
  isMissingRoute, mergeAdopt, missingModels, resultParts, testBody,
} from '../../utils/remotes'

// The state of "connect a remote": the form, the test and the choice of
// models. The form step and the choose step read one of these, so the first
// run can put them in two steps and the sheet can stack them.
//
// Changing the kind, the address or the key drops the test result: the models
// listed were the answer of the old values.
//
// Once the remote is saved the form is locked, because a second create would
// meet the name of the first. A result with a warning or with skipped models
// stays on screen as `outcome`: the person reads it, then adopts the models
// that are missing (never a second create) or continues. A clean result goes
// straight to onDone.
export function useRemoteConnect({ onDone } = {}) {
  const [kind, setKindState] = useState('localai')
  const [preset, setPresetState] = useState('openai')
  const [url, setUrlState] = useState('')
  const [key, setKeyState] = useState('')
  const [name, setNameState] = useState('')
  const [nameEdited, setNameEdited] = useState(false)
  // idle | testing | ok | failed
  const [phase, setPhase] = useState('idle')
  const [models, setModels] = useState([])
  const [failure, setFailure] = useState(null)
  const [ticked, setTicked] = useState([])
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState(null)
  // The notice of a test that listed fewer models than the remote has.
  const [notice, setNotice] = useState('')
  // The saved remote while its result is on screen: the create answer, folded
  // with each adopt after it.
  const [outcome, setOutcome] = useState(null)
  const [adopting, setAdopting] = useState(false)
  const locked = outcome !== null

  const forget = () => { setPhase('idle'); setModels([]); setFailure(null); setTicked([]); setCreateError(null); setNotice('') }

  const nameFor = (k, p, u) => defaultName(k, p, u)

  const setKind = useCallback((next) => {
    if (locked) return
    setKindState(next)
    setUrlState(next === 'openai' ? PRESETS.find(p => p.id === preset)?.url || '' : '')
    setKeyState('')
    if (!nameEdited) setNameState(nameFor(next, preset, next === 'openai' ? PRESETS.find(p => p.id === preset)?.url || '' : ''))
    forget()
  }, [preset, nameEdited, locked])

  const setPreset = useCallback((next) => {
    if (locked) return
    const presetUrl = PRESETS.find(p => p.id === next)?.url || ''
    setPresetState(next)
    setUrlState(presetUrl)
    if (!nameEdited) setNameState(nameFor('openai', next, presetUrl))
    forget()
  }, [nameEdited, locked])

  const setUrl = useCallback((next) => {
    if (locked) return
    setUrlState(next)
    if (!nameEdited) setNameState(nameFor(kind, preset, next))
    forget()
  }, [kind, preset, nameEdited, locked])

  const setKey = useCallback((next) => { if (locked) return; setKeyState(next); forget() }, [locked])
  const setName = useCallback((next) => { if (locked) return; setNameState(next); setNameEdited(true); setCreateError(null) }, [locked])

  // The address of an OpenAI-compatible remote starts as the first preset.
  const effectiveUrl = kind === 'openai' && url === '' && preset !== 'custom'
    ? PRESETS.find(p => p.id === preset)?.url || ''
    : url
  const values = { kind, url: effectiveUrl, key }
  const testable = canTest(values)

  const test = useCallback(async () => {
    setPhase('testing'); setFailure(null); setModels([]); setTicked([]); setCreateError(null); setNotice('')
    try {
      const res = await remotesApi.test(testBody(values))
      if (res?.ok) {
        const list = Array.isArray(res.models) ? res.models : []
        setModels(list)
        setTicked(initialTicked(kind, list))
        setNotice(typeof res.message === 'string' ? res.message : '')
        if (!nameEdited && !name) setNameState(nameFor(kind, preset, effectiveUrl))
        setPhase('ok')
      } else {
        setFailure({ step: failStep(res?.step), message: res?.message || '' })
        setPhase('failed')
      }
    } catch (err) {
      setFailure(isMissingRoute(err) ? { step: 'unsupported', message: '' } : { step: 'other', message: err.message || '' })
      setPhase('failed')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, effectiveUrl, key, preset, name, nameEdited])

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

  const chosen = chosenModels(models, ticked, kind)
  const finalName = (name || nameFor(kind, preset, effectiveUrl)).trim()

  // The answer of a create or an adopt: a clean one ends the flow, any other
  // stays on screen until the person adopts the rest or continues.
  const settle = (result) => {
    const { skipped, warning } = resultParts(result)
    if (!warning && skipped.length === 0) {
      setOutcome(result)
      onDone?.(result)
      return
    }
    setOutcome(result)
  }

  const create = useCallback(async () => {
    // The remote is saved already: adding models is the adopt call, not a
    // second create.
    if (outcome) return null
    setCreating(true); setCreateError(null)
    try {
      const body = { ...testBody(values), name: finalName, models: chosen }
      const result = await remotesApi.create(body)
      settle(result)
      return result
    } catch (err) {
      setCreateError({ kind: createErrorKind(err), message: err.message || '' })
      return null
    } finally {
      setCreating(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, effectiveUrl, key, finalName, ticked, models, onDone, outcome])

  // Adds the chosen models that the saved remote lacks.
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
      setCreateError({ kind: createErrorKind(err), message: err.message || '' })
      return null
    } finally {
      setAdopting(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outcome, ticked, models, onDone])

  // Leaves the result: the remote stays as saved.
  const finish = useCallback(() => { if (outcome) onDone?.(outcome) }, [outcome, onDone])

  return {
    kind, preset, url: effectiveUrl, key, name: finalName, nameValue: name, phase, models, failure, ticked, chosen,
    creating, createError, testable, notice, outcome, locked, adopting, tooMany: ticked.length > MAX_ADOPT,
    adopt, finish,
    setKind, setPreset, setUrl, setKey, setName, test, toggle, setMany, create,
  }
}
