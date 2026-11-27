import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { pluginsApi } from '../utils/pluginsClient'
import { chatPluginRows, pluginCommandList, withPluginOff } from '../utils/chatPlugins'

// The plugins of a chat, with the chat's switch for each: the plugins the
// person turned on (and the chat's agent grants), the ones the chat switched
// off, and the commands of those that are on, for the / menu. The server has
// no list of the commands of a chat, so they come from the package of each
// install, read once per version.
//
// Before the chat exists (no chatId) a switch changes only the screen and
// draftSwitches() gives { plugins_off } for the create request.
//
//   chatId, activeChat   the open chat and its record (plugins_off)
//   agent                the chat's saved agent, or null
//   enabled              the person has the plugins feature and files work
//   onChanged()          a switch went through: the skills list is read again
const packageModels = new Map()

export function useChatPlugins({ chatId = null, activeChat = null, agent = null, enabled = true, onChanged, api = pluginsApi }) {
  const [installs, setInstalls] = useState([])
  const [off, setOff] = useState([])
  const [models, setModels] = useState({})
  const [error, setError] = useState('')
  const offRef = useRef(off)
  offRef.current = off
  const changedRef = useRef(onChanged)
  changedRef.current = onChanged

  useEffect(() => {
    if (!enabled) { setInstalls([]); return undefined }
    const ctl = new AbortController()
    api.installs({}, ctl.signal).then(setInstalls).catch(() => { if (!ctl.signal.aborted) setInstalls([]) })
    return () => ctl.abort()
  }, [enabled, chatId, api])

  // The chat's own list comes with its record; a new chat starts with none off.
  const storedOff = activeChat?.plugins_off
  useEffect(() => {
    setError('')
    setOff(chatId && Array.isArray(storedOff) ? [...storedOff].sort() : [])
  }, [chatId, JSON.stringify(storedOff || [])]) // eslint-disable-line react-hooks/exhaustive-deps

  const rows = useMemo(() => chatPluginRows(installs, { off, agent }), [installs, off, agent])

  useEffect(() => {
    let live = true
    for (const p of rows) {
      const key = `${p.id}:${p.digest}`
      if (packageModels.has(key)) {
        const m = packageModels.get(key)
        if (m) setModels((s) => (s[p.id] === m ? s : { ...s, [p.id]: m }))
        continue
      }
      packageModels.set(key, null)
      api.detail(p.id)
        .then((d) => { packageModels.set(key, d?.model || null); if (live && d?.model) setModels((s) => ({ ...s, [p.id]: d.model })) })
        .catch(() => packageModels.delete(key))
    }
    return () => { live = false }
  }, [rows, api])

  const commands = useMemo(() => pluginCommandList(rows, models), [rows, models])

  // Switches these installs (an array) on or off in the chat with one request.
  const setManyOn = useCallback(async (ids, on) => {
    const before = offRef.current
    const next = ids.reduce((acc, id) => withPluginOff(acc, id, on), before)
    offRef.current = next
    setOff(next)
    setError('')
    if (!chatId) return
    try {
      const stored = await api.setChatOff(chatId, next)
      offRef.current = stored
      setOff([...stored].sort())
      changedRef.current?.()
    } catch (e) {
      offRef.current = before
      setOff(before)
      setError(e?.message || 'error')
    }
  }, [chatId, api])
  const setOn = useCallback((id, on) => setManyOn([id], on), [setManyOn])

  const draftSwitches = useCallback(() => (!chatId && offRef.current.length ? { plugins_off: offRef.current } : null), [chatId])

  return { rows, commands, models, setOn, setManyOn, error, draftSwitches }
}
