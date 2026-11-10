import { useMemo } from 'react'
import { useAuth } from '../../context/AuthContext'
import { useWellKnown } from '../../hooks/useWellKnown'
import { filesApi } from '../../utils/api'
import { background, backgroundStyle } from '../../utils/wellKnown'
import './wellKnown.css'

// BackgroundLayer draws the background that a key of ui.json (homeBackground
// or chatBackground) names, in the box of the page that holds it and under that
// page's content. testId names the layer for tests: `${testId}`, `-art` and
// `-scrim`. The file changes live (the change feed re-reads it), whether the
// assistant or the Appearance tab wrote it.
export default function BackgroundLayer({ bgKey, testId }) {
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const { doc } = useWellKnown('ui', { enabled: on })
  const data = doc?.data
  const style = useMemo(
    () => backgroundStyle(background(data, bgKey), (p) => filesApi.contentUrl(`/${p}`, true)),
    [data, bgKey],
  )
  if (!style) return null
  return (
    <div className="bg-layer" aria-hidden="true" data-testid={testId}>
      <div className="bg-layer__art" style={style.art} data-testid={`${testId}-art`} />
      <div className="bg-layer__scrim" style={style.scrim} data-testid={`${testId}-scrim`} />
    </div>
  )
}
