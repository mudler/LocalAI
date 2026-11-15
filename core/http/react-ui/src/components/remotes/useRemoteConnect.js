import { useCallback, useState } from 'react'
import { remotesApi } from '../../utils/api'
import {
  PRESETS, canTest, chosenModels, createErrorKind, defaultName, failStep, initialTicked, testBody,
} from '../../utils/remotes'

// The state of "connect a remote": the form, the test and the choice of
// models. The form step and the choose step read one of these, so the first
// run can put them in two steps and the sheet can stack them.
//
// Changing the kind, the address or the key drops the test result: the models
// listed were the answer of the old values.
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

  const forget = () => { setPhase('idle'); setModels([]); setFailure(null); setTicked([]); setCreateError(null) }

  const nameFor = (k, p, u) => defaultName(k, p, u)

  const setKind = useCallback((next) => {
    setKindState(next)
    setUrlState(next === 'openai' ? PRESETS.find(p => p.id === preset)?.url || '' : '')
    setKeyState('')
    if (!nameEdited) setNameState(nameFor(next, preset, next === 'openai' ? PRESETS.find(p => p.id === preset)?.url || '' : ''))
    forget()
  }, [preset, nameEdited])

  const setPreset = useCallback((next) => {
    const presetUrl = PRESETS.find(p => p.id === next)?.url || ''
    setPresetState(next)
    setUrlState(presetUrl)
    if (!nameEdited) setNameState(nameFor('openai', next, presetUrl))
    forget()
  }, [nameEdited])

  const setUrl = useCallback((next) => {
    setUrlState(next)
    if (!nameEdited) setNameState(nameFor(kind, preset, next))
    forget()
  }, [kind, preset, nameEdited])

  const setKey = useCallback((next) => { setKeyState(next); forget() }, [])
  const setName = useCallback((next) => { setNameState(next); setNameEdited(true); setCreateError(null) }, [])

  // The address of an OpenAI-compatible remote starts as the first preset.
  const effectiveUrl = kind === 'openai' && url === '' && preset !== 'custom'
    ? PRESETS.find(p => p.id === preset)?.url || ''
    : url
  const values = { kind, url: effectiveUrl, key }
  const testable = canTest(values)

  const test = useCallback(async () => {
    setPhase('testing'); setFailure(null); setModels([]); setTicked([]); setCreateError(null)
    try {
      const res = await remotesApi.test(testBody(values))
      if (res?.ok) {
        const list = Array.isArray(res.models) ? res.models : []
        setModels(list)
        setTicked(initialTicked(kind, list))
        if (!nameEdited && !name) setNameState(nameFor(kind, preset, effectiveUrl))
        setPhase('ok')
      } else {
        setFailure({ step: failStep(res?.step), message: res?.message || '' })
        setPhase('failed')
      }
    } catch (err) {
      setFailure({ step: 'other', message: err.message || '' })
      setPhase('failed')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, effectiveUrl, key, preset, name, nameEdited])

  const toggle = useCallback((id) => {
    setTicked(list => (list.includes(id) ? list.filter(x => x !== id) : [...list, id]))
  }, [])
  const setMany = useCallback((ids, on) => {
    setTicked(list => {
      const set = new Set(list)
      for (const id of ids) { if (on) set.add(id); else set.delete(id) }
      return [...set]
    })
  }, [])

  const chosen = chosenModels(models, ticked)
  const finalName = (name || nameFor(kind, preset, effectiveUrl)).trim()

  const create = useCallback(async () => {
    setCreating(true); setCreateError(null)
    try {
      const body = { ...testBody(values), name: finalName, models: chosen }
      const result = await remotesApi.create(body)
      onDone?.(result)
      return result
    } catch (err) {
      setCreateError({ kind: createErrorKind(err), message: err.message || '' })
      return null
    } finally {
      setCreating(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, effectiveUrl, key, finalName, ticked, models, onDone])

  return {
    kind, preset, url: effectiveUrl, key, name: finalName, nameValue: name, phase, models, failure, ticked, chosen,
    creating, createError, testable,
    setKind, setPreset, setUrl, setKey, setName, test, toggle, setMany, create,
  }
}
