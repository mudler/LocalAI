/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// "Send my reminders and run results to": a choice among the people and the
// channels the bot already knows, with the raw ID as a fallback for a saved ID
// it does not know and for one the owner types. The saved field does not
// change: notify_chat is the ID of the chat.
import { useState } from 'react'
import { Field, Select, TextInput } from './controls'

const OTHER = '\u0000other'

// The label of a target: "Ada Example · U001", or the ID alone.
const labelOf = (x) => (x.name ? `${x.name} · ${x.id}` : x.id)

/**
 * @param {object} props
 * @param {object} props.form
 * @param {Function} props.setForm
 * @param {{people: Array, channels: Array}} props.known  from knownTargets
 * @param {string} props.label
 * @param {string} props.hint
 * @param {string} props.inputMode  the input mode of the raw ID
 * @param {Function} props.s        the shared words
 */
export default function NotifyField({ form, setForm, known, label, hint, inputMode = 'text', s }) {
  const value = form.notify_chat || ''
  const ids = new Set([...known.people, ...known.channels].map((x) => x.id))
  const unknown = value !== '' && !ids.has(value)
  const [typing, setTyping] = useState(false)
  const choice = typing ? OTHER : value
  return (
    <Field label={label} hint={hint} optional>
      {(p) => (
        <>
          <Select {...p} data-testid="connector-notify" value={choice}
            onChange={(e) => {
              if (e.target.value === OTHER) { setTyping(true); return }
              setTyping(false)
              setForm({ ...form, notify_chat: e.target.value })
            }}>
            <option value="">{s('notify.none')}</option>
            {known.people.length > 0 && (
              <optgroup label={s('notify.people')}>{known.people.map((x) => <option key={x.id} value={x.id}>{labelOf(x)}</option>)}</optgroup>
            )}
            {known.channels.length > 0 && (
              <optgroup label={s('notify.channels')}>{known.channels.map((x) => <option key={x.id} value={x.id}>{labelOf(x)}</option>)}</optgroup>
            )}
            {unknown && !typing && <option value={value}>{s('notify.saved', { id: value })}</option>}
            <option value={OTHER}>{s('notify.other')}</option>
          </Select>
          {typing && (
            <TextInput mono inputMode={inputMode} data-testid="connector-notify-other" aria-label={s('notify.otherLabel')} value={value}
              onChange={(e) => setForm({ ...form, notify_chat: e.target.value })} />
          )}
        </>
      )}
    </Field>
  )
}
