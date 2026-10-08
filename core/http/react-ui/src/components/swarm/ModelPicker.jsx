import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useModels } from '../../hooks/useModels'

const LIMIT = 50

// A model name, typed or picked. A rule may be written before its model is
// installed, so what is typed is kept as it is: the list only helps. Aliases
// are offered beside models, each with the model it resolves to.
//
//   hints   { [name]: 'alias of x' } shown under a name and searched with it
export default function ModelPicker({ id, value, onChange, hints = {} }) {
  const { t } = useTranslation('swarm')
  const { models } = useModels()
  const listId = useId()
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const wrapRef = useRef(null)

  useEffect(() => {
    const away = event => { if (wrapRef.current && !wrapRef.current.contains(event.target)) setOpen(false) }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [])

  const needle = (value || '').toLowerCase()
  const matches = useMemo(() => {
    const names = [...new Set([...models.map(m => m.id), ...Object.keys(hints)])]
    return names.filter(name => name.toLowerCase().includes(needle) || (hints[name] || '').toLowerCase().includes(needle)).slice(0, LIMIT)
  }, [models, hints, needle])

  const pick = name => { onChange(name); setOpen(false); setActive(-1) }

  const onKeyDown = event => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      if (!open) { setOpen(true); return }
      setActive(i => Math.min(i + 1, matches.length - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActive(i => Math.max(i - 1, 0))
    } else if (event.key === 'Enter' && open) {
      event.preventDefault()
      pick(active >= 0 ? matches[active] : (matches.length > 0 && !matches.includes(value) ? matches[0] : value))
    } else if (event.key === 'Escape' && open) {
      event.preventDefault()
      setOpen(false)
      setActive(-1)
    }
  }

  return (
    <div className="sw-picker" ref={wrapRef}>
      <input
        id={id}
        className="dk-input dk-input--mono"
        role="combobox"
        aria-expanded={open}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={active >= 0 ? `${listId}-${active}` : undefined}
        autoComplete="off"
        spellCheck={false}
        placeholder={t('sheet.modelPlaceholder')}
        value={value}
        onChange={event => { onChange(event.target.value); setOpen(true); setActive(-1) }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
      />
      {open && (
        <div className="dk-cmdlist sw-picker__list" role="listbox" id={listId} aria-label={t('sheet.model')}>
          {matches.map((name, index) => (
            <div
              key={name}
              id={`${listId}-${index}`}
              className="dk-cmd"
              role="option"
              aria-selected={index === active}
              onMouseDown={event => { event.preventDefault(); pick(name) }}
              onMouseEnter={() => setActive(index)}
            >
              <span className="dk-cmd-main">
                <span className="dk-cmd-name dk-mono">{name}</span>
                {hints[name] && <span className="dk-cmd-desc">{hints[name]}</span>}
              </span>
            </div>
          ))}
          {matches.length === 0 && <div className="dk-cmd-empty">{t('sheet.pickerEmpty', { name: value })}</div>}
        </div>
      )}
    </div>
  )
}
