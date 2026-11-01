import { useId, useState } from 'react'
import Icon from './Icon'
import { addEntry, removeEntry } from '../utils/chatAgents'
import '../pages/chatskills.css'
import '../pages/chatauto.css'

// A list of names that an agent grants: chips, and a field with suggestions. A
// name that is not in the suggestions is still allowed: the server decides what
// the owner may use when the chat runs.
export default function NameList({ label, hint, value, onChange, suggestions, testId }) {
  const [text, setText] = useState('')
  const listId = useId()
  const add = () => { onChange(addEntry(value, text)); setText('') }
  return (
    <div className="cs-field" data-testid={testId}>
      <span>{label}</span>
      {value.length > 0 && (
        <ul className="ca-chips" aria-label={label}>
          {value.map((v) => (
            <li key={v} className="ca-chip">
              <span>{v}</span>
              <button type="button" className="ca-chip__x" aria-label={`${v} ×`} onClick={() => onChange(removeEntry(value, v))}>
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="ca-add">
        <input className="input" list={listId} value={text} aria-label={`${label} +`} placeholder={hint}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add() } }} />
        <datalist id={listId}>
          {(suggestions || []).filter((s) => !value.includes(s)).map((s) => <option key={s} value={s} />)}
        </datalist>
        <button type="button" className="btn btn-secondary btn-sm" onClick={add} disabled={!text.trim()}>+</button>
      </div>
    </div>
  )
}

