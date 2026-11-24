import { useCallback } from 'react'
import { useWellKnown, wellKnownStore } from './useWellKnown'
import { seenOf } from '../utils/discovery'

// useSeen reads the seen record of user.json (what LocalAI showed and what the
// user answered) and changes it. update(fn) applies fn to the newest record and
// saves the whole record from the version it read. The server's hint service
// writes the same record, so a conflict reads the file again and applies fn
// once more on top of the newer record.
export function useSeen({ enabled = true } = {}) {
  const wk = useWellKnown('user', { enabled })
  const update = useCallback(async (fn) => {
    for (let attempt = 0; ; attempt++) {
      const doc = wellKnownStore.snapshot('user').doc || await wellKnownStore.load('user')
      const next = fn(seenOf(doc))
      try {
        return await wellKnownStore.save('user', { seen: next }, { version: doc?.version ?? 0 })
      } catch (e) {
        if (!e?.conflict || attempt > 0) throw e
        await wellKnownStore.load('user')
      }
    }
  }, [])
  return { seen: seenOf(wk.doc), doc: wk.doc, loaded: !wk.loading, error: wk.error, update }
}
