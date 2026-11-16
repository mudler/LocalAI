import { createAutoscroll } from './autoscroll.js'
import { createIcon } from './icons.js'

export function createTimeline(R) {
  const h = R.createElement, Icon = createIcon(R)
  return function Timeline({ children, activity }) {
    const pane = R.useRef(null), content = R.useRef(null), controller = R.useRef(null)
    const [following, setFollowing] = R.useState(true)
    R.useEffect(() => {
      const scroller = createAutoscroll(pane.current, setFollowing)
      controller.current = scroller
      // Resize catches delayed previews; mutations catch new and streaming text.
      const resize = new ResizeObserver(scroller.follow)
      const mutations = new MutationObserver(scroller.follow)
      resize.observe(content.current)
      resize.observe(pane.current)
      mutations.observe(content.current, { childList: true, subtree: true, characterData: true })
      scroller.follow()
      return () => { resize.disconnect(); mutations.disconnect(); scroller.dispose(); controller.current = null }
    }, [])
    return <main id="pipeline-status" className="ap-status-pane" aria-label="Pipeline status and outputs" tabIndex="-1">
      <div className="ap-timeline" ref={pane} onScroll={() => controller.current?.scroll()} tabIndex="0" aria-label="Output history">
        <div ref={content}>{children}</div>
      </div>
      {activity}
      <footer className="ap-timeline-footer"><label className={`ap-autoscroll ${following ? 'ap-autoscroll-on' : ''}`}>
        <input type="checkbox" checked={following} onChange={event => controller.current?.toggle(event.target.checked)} />
        <Icon name="down" />Autoscroll
      </label></footer>
    </main>
  }
}
