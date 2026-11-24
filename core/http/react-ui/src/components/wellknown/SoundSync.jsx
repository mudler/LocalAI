import { useEffect } from 'react'
import { useAuth } from '../../context/AuthContext'
import { useWellKnown } from '../../hooks/useWellKnown'
import { soundOf } from '../../utils/sound'
import { listenForGesture, setSound } from '../../utils/soundPlayer'

// SoundSync keeps the sound settings of ui.json where the player reads them,
// whoever changed the file, and waits for the first input of the person: no
// sound plays before that. Sound is off unless the file turns it on.
export default function SoundSync() {
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const { doc } = useWellKnown('ui', { enabled: on })
  useEffect(() => { listenForGesture() }, [])
  useEffect(() => { setSound(soundOf(doc?.data)) }, [doc])
  return null
}
