import { useState, useRef, useEffect } from 'react'

import { suggestKeys, suggestValues } from '../../utils/nodeLabelSuggestions'

/**
 * Controlled chip-builder for { key: value } maps. Replaces the prior
 * comma-separated-string Node Selector input AND the bespoke Labels editor
 * in the node drawer - both were rendering the same chip pattern with
 * subtly different markup.
 *
 * Fully controlled: parent owns the map and decides what onAdd/onRemove
 * does (form state for the scheduling form; API calls for the live
 * labels editor). The component just renders chips and a key/value input
 * row.
 *
 * With `suggestions` it also completes what the user types against the
 * vocabulary the cluster actually uses. That is where label discovery lives on
 * the scheduling page: labels only matter while a selector is being written,
 * so browsing them belongs in the field rather than in a card standing open
 * above the rules. The suggestions are never a constraint - a key no node
 * reports yet still commits as typed, which is the workflow of writing a rule
 * before labelling the nodes for it.
 *
 * Props:
 *   pairs       - current map of key -> value
 *   onAdd(k,v)  - called when the user adds a pair (parent handles dedup
 *                 and persistence side effects)
 *   onRemove(k) - called when a chip's × is clicked
 *   placeholderKey, placeholderValue - input hints
 *   ariaLabel   - accessible name for the section
 *   ariaLabelKey, ariaLabelValue - accessible names for the two inputs
 *   addLabel    - accessible name for the commit button
 *   suggestions - label index from utils/nodeLabelSuggestions; omit for none
 */
export default function KeyValueChips({
  pairs, onAdd, onRemove,
  placeholderKey = 'key', placeholderValue = 'value',
  ariaLabel, ariaLabelKey = 'Key', ariaLabelValue = 'Value',
  addLabel = 'Add', suggestions,
}) {
  const [k, setK] = useState('')
  const [v, setV] = useState('')
  // Which input owns the open list, and which of its options is armed for
  // Enter. -1 means the user is typing free text and Enter should commit the
  // pair rather than pick anything.
  const [openField, setOpenField] = useState(null)
  const [active, setActive] = useState(-1)
  const rowRef = useRef(null)

  const entries = pairs ? Object.entries(pairs) : []

  const options = !suggestions || !openField
    ? []
    : openField === 'key'
      ? suggestKeys(suggestions, k, entries.map(([key]) => key))
      : suggestValues(suggestions, k.trim(), v)

  // A click anywhere else is a dismissal. Without this the list survives the
  // user moving on to the rest of the form and covers it.
  useEffect(() => {
    if (!openField) return undefined
    const onDocumentPointerDown = (event) => {
      if (!rowRef.current?.contains(event.target)) setOpenField(null)
    }
    document.addEventListener('mousedown', onDocumentPointerDown)
    return () => document.removeEventListener('mousedown', onDocumentPointerDown)
  }, [openField])

  const add = () => {
    const key = k.trim()
    if (!key) return
    onAdd(key, v.trim())
    setK(''); setV('')
    setOpenField(null); setActive(-1)
  }

  const pick = (field, option) => {
    if (field === 'key') setK(option)
    else setV(option)
    setOpenField(null)
    setActive(-1)
  }

  const onKeyDown = (field) => (e) => {
    const open = openField === field && options.length > 0
    if (e.key === 'ArrowDown' && open) {
      e.preventDefault()
      setActive(current => (current + 1) % options.length)
      return
    }
    if (e.key === 'ArrowUp' && open) {
      e.preventDefault()
      setActive(current => (current <= 0 ? options.length - 1 : current - 1))
      return
    }
    if (e.key === 'Escape' && openField) {
      e.preventDefault()
      setOpenField(null)
      setActive(-1)
      return
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      // Enter completes the suggestion the user armed, and commits the pair
      // otherwise. Committing a half-typed key because a list happened to be
      // open is the error this ordering avoids.
      if (open && active >= 0) pick(field, options[active])
      else add()
    }
  }

  const listId = 'kvchips-suggestions'
  const inputProps = (field, value, setValue, placeholder, label) => ({
    className: 'input flex-1',
    type: 'text',
    role: suggestions ? 'combobox' : undefined,
    'aria-expanded': suggestions ? openField === field : undefined,
    'aria-controls': suggestions && openField === field ? listId : undefined,
    'aria-autocomplete': suggestions ? 'list' : undefined,
    'aria-label': label,
    placeholder,
    value,
    onChange: (e) => {
      setValue(e.target.value)
      if (suggestions) { setOpenField(field); setActive(-1) }
    },
    onFocus: () => { if (suggestions) { setOpenField(field); setActive(-1) } },
    onKeyDown: onKeyDown(field),
  })

  return (
    <div aria-label={ariaLabel}>
      {entries.length > 0 && (
        <div className="kvchips__chips">
          {entries.map(([key, val]) => (
            <span key={key} className="kvchips__chip">
              {key}={val}
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); onRemove(key) }}
                aria-label={`Remove ${key}`}
                title="Remove"
                className="kvchips__chip-remove"
              >
                <i className="fas fa-times" />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="kvchips__row" ref={rowRef}>
        <input {...inputProps('key', k, setK, placeholderKey, ariaLabelKey)} />
        <input {...inputProps('value', v, setV, placeholderValue, ariaLabelValue)} />
        <button
          type="button"
          className="btn btn-secondary btn-sm kvchips__add"
          onClick={add}
          disabled={!k.trim()}
          aria-label={addLabel}
        >
          <i className="fas fa-plus" /> Add
        </button>
        {options.length > 0 && (
          <ul className="kvchips__suggestions" id={listId} role="listbox" data-testid="label-suggestions">
            {options.map((option, index) => (
              <li key={option} role="option" aria-selected={index === active}>
                <button
                  type="button"
                  className={`kvchips__suggestion${index === active ? ' kvchips__suggestion--active' : ''}`}
                  // mousedown, not click: the input's blur would otherwise
                  // close the list before the click ever lands.
                  onMouseDown={(e) => { e.preventDefault(); pick(openField, option) }}
                >
                  {option}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
