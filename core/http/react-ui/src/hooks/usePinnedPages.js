import { useMemo } from 'react'
import { pinsOf, withPin } from '../utils/pages'
import { useWellKnown, wellKnownStore } from './useWellKnown'

// The pinned pages are `pinnedPages` in ui.json of the user's home. ui.json is
// the same copy the theme and the Appearance tab read (wellKnownStore), so a
// pin and an Appearance save always start from the version the other wrote.
// A pin changes only `pinnedPages` and is written from the version it read;
// the server keeps the other keys. When the file changed in between (a 409
// with the newer document: another tab, the assistant), the pin is tried again
// on top of that document.
const MAX_TRIES = 3

export async function setPin(name, pinned) {
  let doc = await wellKnownStore.load('ui')
  for (let i = 0; i < MAX_TRIES; i++) {
    const next = withPin(doc?.data, name, pinned)
    try {
      await wellKnownStore.save('ui', { pinnedPages: next.pinnedPages }, { version: doc?.version ?? 0 })
      return
    } catch (e) {
      if (!e?.conflict) throw e
      doc = e.current
    }
  }
  throw new Error('LocalAI/ui.json keeps changing; try again')
}

export function usePinnedPages(enabled = true) {
  const { doc, error, loading, reload } = useWellKnown('ui', { enabled })
  const pins = useMemo(() => pinsOf(doc?.data), [doc])
  return { pins, loaded: !loading, error: error?.message || '', setPin, refresh: reload }
}
