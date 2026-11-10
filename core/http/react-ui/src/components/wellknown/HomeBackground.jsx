import { useMemo } from 'react'
import { useAuth } from '../../context/AuthContext'
import { useWellKnown } from '../../hooks/useWellKnown'
import { filesApi } from '../../utils/api'
import { homeBackground, homeBackgroundStyle } from '../../utils/wellKnown'
import './wellKnown.css'

// HomeBackground draws the background that homeBackground of ui.json names
// behind the Home page, and nothing on any other page. It sits in the Home
// page's own box and under its content. The file changes live (the change feed
// re-reads it), whether the assistant or the Appearance tab wrote it.
export default function HomeBackground() {
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const { doc } = useWellKnown('ui', { enabled: on })
  const data = doc?.data
  const style = useMemo(
    () => homeBackgroundStyle(homeBackground(data), (p) => filesApi.contentUrl(`/${p}`, true)),
    [data],
  )
  if (!style) return null
  return (
    <div className="home-bg" aria-hidden="true" data-testid="home-bg">
      <div className="home-bg__art" style={style.art} data-testid="home-bg-art" />
      <div className="home-bg__scrim" style={style.scrim} data-testid="home-bg-scrim" />
    </div>
  )
}
