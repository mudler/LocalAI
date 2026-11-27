import { useEffect, useRef } from 'react'
// The real path of the compat stand-in, not '../Icon': the Talking Head add-on
// bundles this file too, and its build answers stand-ins only for its own src/.
import Icon from '../../../../../internal/host-compat/src/components/Icon.jsx'

// The conversation column of a voice page: a heading with Copy, a line for an
// empty conversation, and the turns the caller draws (see Turn). It scrolls
// its own box to the newest turn whenever `follow` changes. The words come
// from the caller, so the Talk and the Talking Head pages share it.
export default function Transcript({ title, copyLabel, onCopy, canCopy, empty, testId, copyTestId, follow, children }) {
  const endRef = useRef(null)

  // Scroll the transcript's own overflow container. scrollIntoView bubbles to
  // every scrollable ancestor (incl. the window), which yanked the whole page
  // down to the transcript box on mount; scoping to the box avoids it.
  useEffect(() => {
    const box = endRef.current?.parentElement
    box?.scrollTo({ top: box.scrollHeight, behavior: 'smooth' })
  }, [follow])

  return (
    <section className="talk-transcript" aria-label={title}>
      <div className="talk-transcript__head">
        <h2>{title}</h2>
        <button type="button" className="talk-btn talk-btn--ghost" onClick={onCopy} disabled={!canCopy} data-testid={copyTestId}>
          <Icon name="copy" /> {copyLabel}
        </button>
      </div>
      <div className="talk-transcript__body" data-testid={testId}>
        {empty && <p className="talk-transcript__empty">{empty}</p>}
        {children}
        <div ref={endRef} />
      </div>
    </section>
  )
}

// One turn: the speaker's label in the margin and the text. `textClass` styles
// the text (a tool call, say) and `children` go under it.
export function Turn({ role, label, text, textClass, children }) {
  return (
    <div className="talk-turn" data-role={role}>
      <b>{label}</b>
      <p className={textClass}>{text}</p>
      {children}
    </div>
  )
}
