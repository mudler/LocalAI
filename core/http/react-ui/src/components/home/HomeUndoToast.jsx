import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { undoStyle } from './memory'
import Icon from '../Icon'

// The kit's undo toast. The bar counts the time down; hover and focus pause the
// bar in CSS, and the timer here pauses with it. When the time ends, or the
// toast is dismissed, onExpire runs and the action becomes final.
export default function HomeUndoToast({ message, onUndo, onExpire, duration = 6000 }) {
  const { t } = useTranslation('home')
  const ref = useRef(null)
  const left = useRef(duration)
  const startedAt = useRef(0)
  const timer = useRef(null)
  const expire = useRef(onExpire)
  expire.current = onExpire

  const run = useCallback(() => {
    clearTimeout(timer.current)
    startedAt.current = Date.now()
    timer.current = setTimeout(() => expire.current(), left.current)
  }, [])

  const pause = useCallback(() => {
    if (!timer.current) return
    clearTimeout(timer.current)
    timer.current = null
    left.current = Math.max(500, left.current - (Date.now() - startedAt.current))
  }, [])

  useEffect(() => {
    run()
    return () => clearTimeout(timer.current)
  }, [run])

  return (
    <div className="dk-toast-region">
      <div
        ref={ref}
        className="dk-toast dk-toast--undo"
        role="status"
        style={undoStyle(duration)}
        data-testid="home-undo-toast"
        onMouseEnter={pause}
        onMouseLeave={() => { if (!timer.current) run() }}
        onFocus={pause}
        onBlur={(e) => { if (!ref.current.contains(e.relatedTarget) && !timer.current) run() }}
      >
        <span className="dk-toast-text">{message}</span>
        <button className="dk-toast-action" type="button" onClick={onUndo}>{t('jump.undo')}</button>
        <button className="dk-toast-close" type="button" aria-label={t('jump.dismissToast')} onClick={() => expire.current()}>
          <Icon name="close" />
        </button>
        <span className="dk-toast-bar" aria-hidden="true" />
      </div>
    </div>
  )
}
