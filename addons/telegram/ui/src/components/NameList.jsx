import { useState } from 'react'
import Icon from '../../../../internal/host-compat/src/components/Icon'

// A list of senders as chips, with a field to add one. Entries are trimmed and
// kept once; the server checks their shape.
export default function NameList({ label, hint, value, onChange, testId }) {
  const [text, setText] = useState('')
  const add = () => {
    const v = text.trim()
    if (v && !value.includes(v)) onChange([...value, v])
    setText('')
  }
  return (
    <div className="tg-field" data-testid={testId}>
      <span>{label}</span>
      {value.length > 0 && (
        <ul className="tg-chips" aria-label={label}>
          {value.map((v) => (
            <li key={v} className="tg-chip">
              <span>{v}</span>
              <button type="button" className="tg-chip__x" aria-label={`${v} ×`} onClick={() => onChange(value.filter((x) => x !== v))}>
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="tg-add">
        <input className="input" value={text} aria-label={`${label} +`} placeholder={hint}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add() } }} />
        <button type="button" className="btn btn-secondary btn-sm" onClick={add} disabled={!text.trim()}>+</button>
      </div>
    </div>
  )
}
