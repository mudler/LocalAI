/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef, useState } from 'react'
import Popover from '../Popover'
import { Button } from '../ui'

// One "Filters" button for a list, with every refinement in a popover (a
// bottom sheet on a phone). The controls are native radios and checkboxes:
// arrows move inside a group, Tab moves between groups, Space picks, and
// Escape or a click outside closes and returns focus to the button.
//
//   groups   [{ id, legend, kind: 'radio', value, onChange(value), options: [{ value, label, count }] }
//            | { id, legend, kind: 'check', options: [{ key, label, count, checked, onChange(checked) }] }]
//   active   number of refinements that differ from the default, shown on the button
//   labels   { button, title, done }
export default function BackendFilters({ groups, active = 0, labels, testId }) {
  const triggerRef = useRef(null)
  const bodyRef = useRef(null)
  const [open, setOpen] = useState(false)
  const baseId = useId()
  // The page answers a pick by changing the URL, which can land a moment
  // later. Until it does, the menu shows the pick, so the control never
  // flips back under the pointer. An overlay holds only while the page's
  // value is the one it was made from.
  const [picks, setPicks] = useState({})
  const shownValue = (id, value) => (picks[id] && picks[id].from === value ? picks[id].to : value)
  const pick = (id, from, to) => setPicks(current => ({ ...current, [id]: { from, to } }))
  // Once the page's values move, it has answered: drop every overlay, so an
  // old one cannot come back when a value later returns to where it was.
  const values = JSON.stringify(groups.filter(Boolean).map(g => (g.kind === 'radio' ? g.value : g.options.map(o => !!o.checked))))
  useEffect(() => { setPicks({}) }, [values])

  // Land on the picked option of the first group, so a keyboard user starts
  // where the arrows make sense.
  useEffect(() => {
    if (!open) return undefined
    const raf = requestAnimationFrame(() => {
      const body = bodyRef.current
      const first = body?.querySelector('input:checked') || body?.querySelector('input')
      first?.focus({ preventScroll: true })
    })
    return () => cancelAnimationFrame(raf)
  }, [open])

  const close = () => setOpen(false)
  const shown = groups.filter(g => g && g.options.length > 0)

  return (
    <>
      <Button
        ref={triggerRef}
        icon="filter"
        className="bk-filter-btn"
        aria-haspopup="dialog"
        aria-expanded={open}
        data-testid={testId}
        onClick={() => setOpen(v => !v)}
      >
        {labels.button}
        {active > 0 && <span className="bk-filter-btn__n" aria-hidden="true">{active}</span>}
      </Button>
      <Popover anchor={triggerRef} open={open} onClose={close} ariaLabel={labels.title} className="bk-filter-pop">
        <div className="bk-filter-pop__head">
          <h2 className="bk-filter-pop__title">{labels.title}</h2>
          <Button variant="primary" size="sm" onClick={() => { close(); triggerRef.current?.focus() }}>{labels.done}</Button>
        </div>
        <div className="bk-filter-pop__body" ref={bodyRef}>
          {shown.map(group => (
            <fieldset key={group.id} className="bk-filter-group">
              <legend className="bk-filter-group__legend">{group.legend}</legend>
              {group.options.map(option => {
                const radio = group.kind === 'radio'
                const key = radio ? option.value : option.key
                const checkId = `${group.id}:${option.key}`
                const checked = radio
                  ? shownValue(group.id, group.value) === option.value
                  : shownValue(checkId, !!option.checked)
                return (
                  <label key={key || 'all'} className="bk-filter-opt">
                    <input
                      type={radio ? 'radio' : 'checkbox'}
                      className={radio ? 'dk-radio' : 'dk-check'}
                      name={radio ? `${baseId}-${group.id}` : undefined}
                      checked={checked}
                      onChange={(e) => {
                        if (radio) {
                          pick(group.id, group.value, option.value)
                          group.onChange(option.value)
                        } else {
                          pick(checkId, !!option.checked, e.target.checked)
                          option.onChange(e.target.checked)
                        }
                      }}
                    />
                    <span className="bk-filter-opt__label">{option.label}</span>
                    {option.count !== undefined && <span className="bk-filter-opt__count">{option.count}</span>}
                  </label>
                )
              })}
            </fieldset>
          ))}
        </div>
      </Popover>
    </>
  )
}
