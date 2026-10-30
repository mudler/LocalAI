/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'
import { Field } from './controls'

// A list of senders as chips, with a field to add one: Enter or a comma adds
// the entry. Entries are trimmed and kept once; the server checks their shape.
export default function NameList({ label, hint, value, onChange, testId, addLabel = 'Add' }) {
  const [text, setText] = useState('')
  const add = () => {
    const v = text.trim()
    if (v && !value.includes(v)) onChange([...value, v])
    setText('')
  }
  return (
    <div data-testid={testId}>
      <Field label={label}>
        {(p) => (
          <>
            {value.length > 0 && (
              <ul className="cn-chips" aria-label={label}>
                {value.map((v) => (
                  <li key={v} className="dk-chip dk-chip--sm">
                    <span>{v}</span>
                    <button type="button" className="dk-chip-x" aria-label={`${v} ×`} onClick={() => onChange(value.filter((x) => x !== v))}>
                      <Icon name="close" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="cn-add">
              <input {...p} className="dk-input" value={text} aria-label={`${label} +`} placeholder={hint}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add() } }} />
              <button type="button" className="dk-btn dk-btn--secondary" onClick={add} disabled={!text.trim()}>{addLabel}</button>
            </div>
          </>
        )}
      </Field>
    </div>
  )
}
